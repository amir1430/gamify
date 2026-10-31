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
	rows := ok("/api/users/" + id + "/activity?game=arena&limit=10")
	if len(rows["items"].([]any)) != 2 || rows["next"] != nil {
		t.Fatalf("rows = %v", rows)
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
	for _, k := range []string{"name", "max_amount", "max_payout", "variants", "pvp", "only", "enabled", "overrides", "effective"} {
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

// The lists' search and filters: free text, a time range, and the adjustments' other columns.
func TestListFilters(t *testing.T) {
	a, adm, srv, c, alice := readEnv(t)
	ctx := context.Background()
	ok := func(path string) []any {
		t.Helper()
		st, body := call(t, c, srv, "GET", path, nil)
		if st != 200 {
			t.Fatalf("GET %s: %d %v", path, st, body)
		}
		return body["items"].([]any)
	}
	bad := func(path string) {
		t.Helper()
		st, body := call(t, c, srv, "GET", path, nil)
		wantErr(t, path, st, body, 400, "bad_request")
	}

	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.audit(ctx, tx, nil, nil, "game.set", "game:Arena", nil, nil, "Weekend promo"); err != nil {
		t.Fatal(err)
	}
	if err := adm.audit(ctx, tx, nil, nil, "game.set", "game:dice", nil, nil, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pool.Exec(ctx, `UPDATE admin_audit SET created_at = '2020-01-02T00:00:00Z' WHERE target = 'game:dice'`); err != nil {
		t.Fatal(err)
	}
	// q: part of the target, reason or action, any case; % and _ are literal.
	for q, want := range map[string]int{"arena": 1, "PROMO": 1, "game.set": 2, "100%25": 0, "game_": 0} {
		if n := len(ok("/api/audit?q=" + q)); n != want {
			t.Fatalf("audit q=%s: %d rows, want %d", q, n, want)
		}
	}
	if n := len(ok("/api/audit?action=game.set&until=2021-01-01T00:00:00Z")); n != 1 {
		t.Fatalf("audit until: %d", n)
	}
	if n := len(ok("/api/audit?action=game.set&since=2021-01-01T00:00:00Z")); n != 1 {
		t.Fatalf("audit since: %d", n)
	}
	bad("/api/audit?since=yesterday")
	bad("/api/audit?until=1")

	bob := a.User("bob")
	addUser(t, a, "op@brand.example", RoleOperator)
	var viewerID, opID int64
	if err := a.Pool.QueryRow(ctx, `SELECT (SELECT id FROM admin_users WHERE email = 'viewer@brand.example'),
		(SELECT id FROM admin_users WHERE email = 'op@brand.example')`).Scan(&viewerID, &opID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pool.Exec(ctx, `INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key, created_at)
		VALUES ($1, 'usd', 500, 'VIP gift', $3, 'done', 'f1', now()), ($2, 'usd', -5, 'fix', $4, 'done', 'f2', '2020-01-02T00:00:00Z'),
		       ($2, 'eur', 7, 'fix', $4, 'done', 'f3', now())`, alice, bob, viewerID, opID); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"currency=usd":               2,
		"currency=eur":               1,
		"requested_by=2":             2,
		"requested_by=1":             1,
		"q=vip":                      1,
		"q=FIX&currency=usd":         1,
		"until=2021-01-01T00:00:00Z": 1,
		"since=2021-01-01T00:00:00Z": 2,
	} {
		if n := len(ok("/api/adjustments?" + path)); n != want {
			t.Fatalf("adjustments %s: %d rows, want %d", path, n, want)
		}
	}
	bad("/api/adjustments?requested_by=x")
	bad("/api/adjustments?since=x")
}

// The treasury: system accounts per currency, what players hold, and any account's history,
// newest first with a running posted balance; a player's wallet history the same way.
func TestTreasury(t *testing.T) {
	a, _, srv, c, alice := readEnv(t)
	ok := func(path string) map[string]any {
		t.Helper()
		st, body := call(t, c, srv, "GET", path, nil)
		if st != 200 {
			t.Fatalf("GET %s: %d %v", path, st, body)
		}
		return body
	}
	bad := func(path string, status int, code string) {
		t.Helper()
		st, body := call(t, c, srv, "GET", path, nil)
		wantErr(t, path, st, body, status, code)
	}

	tr := ok("/api/treasury")
	if tr["history"] != true || len(tr["kinds"].([]any)) == 0 {
		t.Fatalf("treasury = %v", tr)
	}
	usd := tr["currencies"].(map[string]any)["usd"].(map[string]any)
	accs := usd["accounts"].([]any)
	if len(accs) < 9 || accs[0].(map[string]any)["name"] != "house" {
		t.Fatalf("accounts = %v", accs)
	}
	var faucet map[string]any
	for _, x := range accs {
		if x.(map[string]any)["name"] == "faucet" {
			faucet = x.(map[string]any)
		}
	}
	if faucet["net"].(float64) > -10_000 || faucet["debits_posted"].(float64) < 10_000 {
		t.Fatalf("faucet = %v", faucet)
	}
	if usd["players_posted"].(float64) < 10_000 {
		t.Fatalf("players_posted = %v", usd["players_posted"])
	}

	a.Fund(alice, "usd", 250)
	a.Fund(alice, "usd", 50)
	w := ok(fmt.Sprintf("/api/users/%d/wallet?currency=usd&limit=2", alice))
	items := w["items"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 2 || first["kind"] != "top_up" || first["amount"] != 50.0 || first["other"].(map[string]any)["system"] != "faucet" {
		t.Fatalf("wallet = %v", w)
	}
	bal := a.Balance(alice, "usd")
	if first["balance"] != float64(bal) || items[1].(map[string]any)["balance"] != float64(bal-50) {
		t.Fatalf("running balance %v, %v; balance %d", first["balance"], items[1].(map[string]any)["balance"], bal)
	}
	next, _ := w["next"].(string)
	if next == "" {
		t.Fatalf("next = %v", w["next"])
	}
	p2 := ok(fmt.Sprintf("/api/users/%d/wallet?currency=usd&limit=1&before=%s", alice, next))["items"].([]any)
	if p2[0].(map[string]any)["balance"] != float64(bal-300) || p2[0].(map[string]any)["amount"] != 10_000.0 {
		t.Fatalf("page 2 = %v", p2)
	}
	// Filtered: no running balance (entries are skipped).
	f := ok(fmt.Sprintf("/api/users/%d/wallet?currency=usd&kind=charge", alice))["items"].([]any)
	if len(f) != 0 {
		t.Fatalf("charges = %v", f)
	}
	if _, has := ok(fmt.Sprintf("/api/users/%d/wallet?currency=usd&kind=top_up", alice))["items"].([]any)[0].(map[string]any)["balance"]; has {
		t.Fatal("balance on a filtered list")
	}

	h := ok("/api/treasury/usd/faucet?limit=1")["items"].([]any)
	if len(h) != 1 || h[0].(map[string]any)["other"].(map[string]any)["user"] != float64(alice) || h[0].(map[string]any)["amount"] != -50.0 {
		t.Fatalf("faucet history = %v", h)
	}
	bad("/api/treasury/usd/vault", 404, "not_found")
	bad("/api/treasury/xyz/house", 404, "not_found")
	bad(fmt.Sprintf("/api/users/%d/wallet", alice), 400, "bad_request")
	bad(fmt.Sprintf("/api/users/%d/wallet?currency=usd&kind=nope", alice), 400, "bad_request")
	bad(fmt.Sprintf("/api/users/%d/wallet?currency=usd&before=x", alice), 400, "bad_request")
}
