package admin

import (
	"context"
	"fmt"
	"testing"
)

func TestDashboardInsights(t *testing.T) {
	a, _, srv, c, alice := readEnv(t)
	ctx := context.Background()
	bob := a.User("bob")
	a.Transport.SetOnline(alice)
	if _, err := a.Pool.Exec(ctx, `INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key)
		VALUES ($1, 'usd', 500, 'gift', 1, 'pending_approval', 'k1'), ($1, 'usd', 7, 'gift', 1, 'pending_approval', 'k2'),
		       ($2, 'usd', -5, 'fix', 1, 'done', 'k3')`, alice, bob); err != nil {
		t.Fatal(err)
	}

	st, d := call(t, c, srv, "GET", "/api/dashboard", nil)
	if st != 200 {
		t.Fatalf("dashboard: %d %v", st, d)
	}
	if d["online"] != 1.0 {
		t.Fatalf("online = %v", d["online"])
	}
	p := d["players"].(map[string]any)
	if p["total"] != 2.0 || p["new_24h"] != 2.0 || p["active_24h"] != 2.0 || p["new_7d"] != 2.0 {
		t.Fatalf("players = %v", p)
	}
	sign := d["signups"].([]any)
	if len(sign) != 30 || sign[29].(map[string]any)["count"] != 2.0 {
		t.Fatalf("signups = %v", sign)
	}
	adj := d["adjustments"].(map[string]any)
	if adj["pending_approval"] != 2.0 || adj["done"] != 1.0 || adj["failed"] != 0.0 {
		t.Fatalf("adjustments = %v", adj)
	}
	g := d["games"].(map[string]any)
	if g["total"] != 1.0 || g["enabled"] != 1.0 {
		t.Fatalf("games = %v", g)
	}

	st, d = call(t, c, srv, "GET", "/api/dashboard?days=7", nil)
	if st != 200 || len(d["signups"].([]any)) != 7 {
		t.Fatalf("days=7: %d %v", st, d["signups"])
	}
	for _, q := range []string{"days=0", "days=400", "days=x"} {
		st, body := call(t, c, srv, "GET", "/api/dashboard?"+q, nil)
		wantErr(t, q, st, body, 400, "bad_request")
	}
}

func TestOnline(t *testing.T) {
	a, _, srv, c, alice := readEnv(t)
	bob := a.User("bob")
	carol := a.User("carol")
	a.Transport.SetOnline(alice, carol, 999_999) // an ID without a user (deleted, or not ours) is left out

	st, body := call(t, c, srv, "GET", "/api/online", nil)
	if st != 200 {
		t.Fatalf("online: %d %v", st, body)
	}
	if body["total"] != 3.0 || body["connections"] != 3.0 {
		t.Fatalf("online = %v", body)
	}
	items := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first := items[0].(map[string]any)
	if first["id"] != float64(carol) || first["display_name"] != "carol" || first["connections"] != 1.0 ||
		items[1].(map[string]any)["id"] != float64(alice) {
		t.Fatalf("items = %v: want carol then alice (newest first)", items)
	}

	st, body = call(t, c, srv, "GET", "/api/online?limit=1", nil)
	if st != 200 || len(body["items"].([]any)) != 1 || body["total"] != 3.0 {
		t.Fatalf("limit=1: %d %v", st, body)
	}

	// The player list and page say who's connected.
	st, body = call(t, c, srv, "GET", "/api/users", nil)
	if st != 200 {
		t.Fatalf("users: %d %v", st, body)
	}
	conns := map[float64]any{}
	for _, it := range body["items"].([]any) {
		m := it.(map[string]any)
		conns[m["id"].(float64)] = m["connections"]
	}
	if conns[float64(alice)] != 1.0 || conns[float64(bob)] != 0.0 {
		t.Fatalf("users connections = %v", conns)
	}
	st, body = call(t, c, srv, "GET", fmt.Sprintf("/api/users/%d", alice), nil)
	if st != 200 || body["connections"] != 1.0 {
		t.Fatalf("user: %d %v", st, body)
	}
}

func TestSystem(t *testing.T) {
	_, _, srv, c, _ := readEnv(t)
	st, body := call(t, c, srv, "GET", "/api/system", nil)
	if st != 200 {
		t.Fatalf("system: %d %v", st, body)
	}
	for _, k := range []string{"version", "go", "started_at", "runtime", "pool", "health", "currencies", "rpcs", "history", "roles", "console"} {
		if _, has := body[k]; !has {
			t.Fatalf("system lacks %q: %v", k, body)
		}
	}
	h := body["health"].(map[string]any)
	for _, k := range []string{"postgres", "realtime"} {
		chk := h[k].(map[string]any)
		if chk["ok"] != true {
			t.Fatalf("health %s = %v", k, chk)
		}
		if _, has := chk["ms"]; !has {
			t.Fatalf("health %s = %v", k, chk)
		}
	}
	rpcs := body["rpcs"].([]any)
	found := false
	for _, r := range rpcs {
		found = found || r == "seed.rotate"
	}
	if !found {
		t.Fatalf("rpcs = %v: want seed.rotate", rpcs)
	}
	if h := body["history"].([]any); len(h) != 1 || h[0] != "arena" {
		t.Fatalf("history = %v", h)
	}
	roles := body["roles"].(map[string]any)
	if len(roles) != 4 || len(roles["viewer"].([]any)) != 1 || roles["viewer"].([]any)[0] != "view" {
		t.Fatalf("roles = %v", roles)
	}
	cur := body["currencies"].([]any)[0].(map[string]any)
	for _, k := range []string{"code", "decimals", "min_amount", "approve_above"} {
		if _, has := cur[k]; !has {
			t.Fatalf("currency lacks %q: %v", k, cur)
		}
	}
	con := body["console"].(map[string]any)
	if con["fairness"] != true || con["telegram_login"] != false {
		t.Fatalf("console = %v", con)
	}
}
