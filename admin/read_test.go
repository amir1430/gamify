package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/fairness"
)

// readEnv is an admin with fairness and the arena test game registered, a viewer logged in, and
// alice with two arena rows and seeds.
func readEnv(t *testing.T) (a *apptest.App, adm *Admin, srv *httptest.Server, viewer *http.Client, alice int64) {
	t.Helper()
	ctx := context.Background()
	a = apptest.New(t)
	fair, err := fairness.Register(ctx, a.App, apptest.Sealer(t))
	if err != nil {
		t.Fatal(err)
	}
	adm, err = Register(ctx, a.App, Config{Addr: "127.0.0.1:0", Origin: testOrigin, TOTPKey: testKey, Fairness: fair})
	if err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewTLSServer(adm.Handler())
	t.Cleanup(srv.Close)
	viewer = login(t, srv, "viewer@brand.example", addUser(t, a, "viewer@brand.example", RoleViewer))
	alice = a.User("alice")
	a.Fund(alice, "usd", 10_000)
	registerArena(a, arenaRow(2, alice), arenaRow(1, alice))
	if err := fair.Ensure(ctx, alice); err != nil {
		t.Fatal(err)
	}
	return
}

func TestReadEndpoints(t *testing.T) {
	a, adm, srv, c, alice := readEnv(t)
	ctx := context.Background()
	get := func(path string) (int, map[string]any) { return call(t, c, srv, "GET", path, nil) }
	ok := func(path string) map[string]any {
		t.Helper()
		st, body := get(path)
		if st != 200 {
			t.Fatalf("GET %s: %d %v", path, st, body)
		}
		return body
	}
	id := fmt.Sprint(alice)

	d := ok("/api/dashboard")
	if _, has := d["online"]; !has {
		t.Fatalf("dashboard = %v", d)
	}
	usd := d["currencies"].(map[string]any)["usd"].(map[string]any)
	if _, has := usd["system"].(map[string]any)["house"]; !has {
		t.Fatalf("dashboard usd = %v", usd)
	}
	if _, has := usd["rewards_paid"]; !has {
		t.Fatalf("dashboard usd = %v", usd)
	}

	u := ok("/api/users?q=alice")
	items := u["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["display_name"] != "alice" || u["next"] != nil {
		t.Fatalf("users = %v", u)
	}
	one := ok("/api/users/" + id)
	if one["user"].(map[string]any)["display_name"] != "alice" || len(one["identities"].([]any)) != 1 ||
		one["balances"].(map[string]any)["usd"] == nil {
		t.Fatalf("user = %v", one)
	}
	st, body := get("/api/users/999999")
	wantErr(t, "unknown user", st, body, 404, "not_found")
	st, body = get("/api/users/abc")
	wantErr(t, "bad id", st, body, 400, "bad_request")

	games := ok("/api/users/" + id + "/activity")["games"].([]any)
	if len(games) != 1 || games[0] != "arena" {
		t.Fatalf("games = %v", games)
	}
	st, body = get("/api/users/" + id + "/activity?game=nope")
	wantErr(t, "unknown game", st, body, 400, "unknown_game")
	bets := ok("/api/users/" + id + "/activity?game=arena&limit=10")
	if len(bets["items"].([]any)) != 2 || bets["next"] != nil {
		t.Fatalf("bets = %v", bets)
	}
	p1 := ok("/api/users/" + id + "/activity?game=arena&limit=1")
	first := p1["items"].([]any)[0].(map[string]any)
	if len(p1["items"].([]any)) != 1 || p1["next"] != first["id"] {
		t.Fatalf("page 1 = %v", p1)
	}
	p2 := ok(fmt.Sprintf("/api/users/%s/activity?game=arena&limit=1&before=%v", id, p1["next"]))
	if len(p2["items"].([]any)) != 1 || p2["items"].([]any)[0].(map[string]any)["id"] == first["id"] {
		t.Fatalf("page 2 = %v", p2)
	}

	if seeds := ok("/api/users/" + id + "/seeds"); len(seeds["items"].([]any)) == 0 {
		t.Fatalf("seeds = %v", seeds)
	}

	games2 := ok("/api/games")["items"].([]any)
	if len(games2) != 1 {
		t.Fatalf("games = %v", games2)
	}
	gm := games2[0].(map[string]any)
	for _, k := range []string{"name", "max_bet", "max_payout", "variants", "pvp", "only", "enabled", "overrides", "effective"} {
		if _, has := gm[k]; !has {
			t.Fatalf("game lacks %q: %v", k, gm)
		}
	}
	if gm["name"] != "arena" || gm["enabled"] != true {
		t.Fatalf("game = %v", gm)
	}

	// Audit rows carry before/after as JSON, not base64.
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.audit(ctx, tx, nil, nil, "test.change", "thing:1", map[string]int{"x": 1}, map[string]int{"x": 2}, "why"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	au := ok("/api/audit?action=test.change&target=thing:1")["items"].([]any)
	if len(au) != 1 {
		t.Fatalf("audit = %v", au)
	}
	row := au[0].(map[string]any)
	if row["before"].(map[string]any)["x"] != 1.0 || row["after"].(map[string]any)["x"] != 2.0 || row["reason"] != "why" {
		t.Fatalf("audit row = %v", row)
	}
	if n := len(ok("/api/audit?action=login.ok")["items"].([]any)); n != 1 {
		t.Fatalf("login.ok rows = %d", n)
	}
	if n := len(ok("/api/audit?admin=1&action=login.ok")["items"].([]any)); n != 1 {
		t.Fatalf("admin=1 rows = %d", n)
	}
	st, body = get("/api/audit?admin=x")
	wantErr(t, "audit admin", st, body, 400, "bad_request")

	// Adjustments.
	if _, err := a.Pool.Exec(ctx, `INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key)
		VALUES ($1, 'usd', 500, 'gift', 1, 'pending_approval', 'k1'), ($1, 'usd', -5, 'fix', 1, 'done', 'k2')`, alice); err != nil {
		t.Fatal(err)
	}
	if n := len(ok("/api/adjustments")["items"].([]any)); n != 2 {
		t.Fatalf("adjustments = %d", n)
	}
	ad := ok(fmt.Sprintf("/api/adjustments?status=done&user=%d", alice))["items"].([]any)
	if len(ad) != 1 || ad[0].(map[string]any)["amount"] != -5.0 {
		t.Fatalf("adjustments done = %v", ad)
	}
	st, body = get("/api/adjustments?status=nope")
	wantErr(t, "adjustment status", st, body, 400, "bad_request")
}

func TestSeedsNeverUnrevealed(t *testing.T) {
	a, _, srv, c, alice := readEnv(t)
	// Rotate so there is a revealed pair beside the active and next ones.
	if _, cerr := a.Call(alice, "seed.rotate", map[string]string{"client_seed": "mine"}); cerr != nil {
		t.Fatal(cerr)
	}
	st, body := call(t, c, srv, "GET", fmt.Sprintf("/api/users/%d/seeds", alice), nil)
	if st != 200 {
		t.Fatalf("%d %v", st, body)
	}
	revealed := 0
	for _, it := range body["items"].([]any) {
		p := it.(map[string]any)
		seed, _ := p["server_seed"].(string)
		if p["status"] == "revealed" {
			revealed++
			if seed == "" {
				t.Fatalf("revealed pair without its seed: %v", p)
			}
		} else if seed != "" {
			t.Fatalf("unrevealed pair leaks server_seed: %v", p)
		}
	}
	if revealed != 1 {
		t.Fatalf("revealed = %d, want 1", revealed)
	}
}

func TestSeedsWithoutFairness(t *testing.T) {
	a, _, srv := newAdmin(t)
	c := login(t, srv, "viewer@brand.example", addUser(t, a, "viewer@brand.example", RoleViewer))
	st, body := call(t, c, srv, "GET", "/api/users/1/seeds", nil)
	wantErr(t, "no fairness", st, body, 404, "not_found")
}

func TestPaginationLimits(t *testing.T) {
	a, _, srv := newAdmin(t)
	c := login(t, srv, "viewer@brand.example", addUser(t, a, "viewer@brand.example", RoleViewer))
	for i := range 120 {
		a.User(fmt.Sprint("p", i))
	}
	st, body := call(t, c, srv, "GET", "/api/users?limit=1000", nil)
	if st != 200 || len(body["items"].([]any)) != 100 || body["next"] == nil {
		t.Fatalf("limit=1000: %d items %d next %v", st, len(body["items"].([]any)), body["next"])
	}
	st, body = call(t, c, srv, "GET", "/api/users", nil)
	if st != 200 || len(body["items"].([]any)) != 50 {
		t.Fatalf("default limit: %d %v", st, len(body["items"].([]any)))
	}
	for _, q := range []string{"limit=0", "limit=-1", "limit=x", "before=0", "before=-3", "before=x"} {
		st, body := call(t, c, srv, "GET", "/api/users?"+q, nil)
		wantErr(t, q, st, body, 400, "bad_request")
	}
}
