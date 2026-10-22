package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/gameconfig"
)

// writeEnv is an admin with the arena test game registered.
func writeEnv(t *testing.T) (*apptest.App, *Admin, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	a := apptest.New(t)
	fair, err := fairness.Register(ctx, a.App, apptest.Sealer(t))
	if err != nil {
		t.Fatal(err)
	}
	registerArena(a)
	adm, err := Register(ctx, a.App, Config{Addr: "127.0.0.1:0", Origin: testOrigin, TOTPKey: testKey, Fairness: fair})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(adm.Handler())
	t.Cleanup(srv.Close)
	return a, adm, srv
}

func loginAs(t *testing.T, a *apptest.App, srv *httptest.Server, name string, role Role) *http.Client {
	t.Helper()
	email := name + "@brand.example"
	return login(t, srv, email, addUser(t, a, email, role))
}

func auditRows(t *testing.T, a *apptest.App, action string) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLimitWriteAudited(t *testing.T) {
	a, _, srv := writeEnv(t)
	op := loginAs(t, a, srv, "op", RoleOperator)
	support := loginAs(t, a, srv, "support", RoleSupport)

	st, body := call(t, op, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"enabled": true, "max_bet": 500})
	if st != 200 {
		t.Fatalf("set: %d %v", st, body)
	}
	var before, after, target string
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT coalesce(before::text, 'null'), after::text, target FROM admin_audit WHERE action = 'game.limit.set'`).Scan(&before, &after, &target); err != nil {
		t.Fatal(err)
	}
	if before != "null" || !strings.Contains(after, `"max_bet": 500`) || target != "game:arena/usd/" {
		t.Fatalf("audit: before=%s after=%s target=%s", before, after, target)
	}
	if n := len(a.Pushes("feed:config")); n != 1 {
		t.Fatalf("config pushes = %d, want 1", n)
	}
	if got := a.Games.Public()["arena"]["usd"].Limits.MaxBet; got != 500 {
		t.Fatalf("cache not reloaded: max_bet %d", got)
	}

	// The same write again changes nothing: no audit row.
	if st, body := call(t, op, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"enabled": true, "max_bet": 500}); st != 200 {
		t.Fatalf("repeat: %d %v", st, body)
	}
	// Deleting a row that doesn't exist is fine and silent.
	if st, body := call(t, op, srv, "DELETE", "/api/games/arena/limits/tickets/_", nil); st != 204 {
		t.Fatalf("delete missing: %d %v", st, body)
	}
	if n := auditRows(t, a, "game.limit.set"); n != 1 {
		t.Fatalf("set audit rows = %d, want 1", n)
	}
	if n := auditRows(t, a, "game.limit.delete"); n != 0 {
		t.Fatalf("delete audit rows = %d, want 0", n)
	}

	st, body = call(t, op, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"enabled": true, "max_bet": arenaMax + 1})
	wantErr(t, "loosen", st, body, 400, "loosens")
	st, body = call(t, op, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"enabled": true, "max_bet": -5})
	wantErr(t, "bad override", st, body, 400, "bad_override")
	st, body = call(t, op, srv, "PUT", "/api/games/nope/limits/usd/_", map[string]any{"enabled": true})
	wantErr(t, "unknown game", st, body, 404, "not_found")
	st, body = call(t, op, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"max_bet": 5})
	wantErr(t, "no enabled", st, body, 400, "bad_request")
	if n := auditRows(t, a, "game.limit.set"); n != 1 {
		t.Fatalf("audit rows after failures = %d, want 1", n)
	}

	if st, body := call(t, op, srv, "DELETE", "/api/games/arena/limits/usd/_", nil); st != 204 {
		t.Fatalf("delete: %d %v", st, body)
	}
	if n := auditRows(t, a, "game.limit.delete"); n != 1 {
		t.Fatalf("delete audit rows = %d, want 1", n)
	}
	if st, body := call(t, op, srv, "PUT", "/api/games/arena", map[string]any{"enabled": false}); st != 200 {
		t.Fatalf("game off: %d %v", st, body)
	}
	if n := auditRows(t, a, "game.set"); n != 1 {
		t.Fatalf("game.set rows = %d", n)
	}

	st, body = call(t, support, srv, "PUT", "/api/games/arena/limits/usd/_", map[string]any{"enabled": true})
	wantErr(t, "support", st, body, 403, "forbidden")
}

func TestUnlinkNeedsReason(t *testing.T) {
	a, _, srv := writeEnv(t)
	support := loginAs(t, a, srv, "support", RoleSupport)
	alice := a.User("alice")
	if err := a.Users.Link(context.Background(), alice, auth.Identity{Provider: "telegram", Subject: "42"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/users/" + itoa(alice) + "/identities/telegram"
	st, body := call(t, support, srv, "DELETE", path, nil)
	wantErr(t, "no body", st, body, 400, "reason_required")
	st, body = call(t, support, srv, "DELETE", path, map[string]string{"reason": "  "})
	wantErr(t, "blank", st, body, 400, "reason_required")
	if st, body := call(t, support, srv, "DELETE", path, map[string]string{"reason": "stolen"}); st != 204 {
		t.Fatalf("unlink: %d %v", st, body)
	}
	if n := auditRows(t, a, "user.unlink"); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
	st, body = call(t, support, srv, "DELETE", path, map[string]string{"reason": "again"})
	wantErr(t, "gone", st, body, 404, "not_found")
	st, body = call(t, support, srv, "DELETE", "/api/users/"+itoa(alice)+"/identities/apptest", map[string]string{"reason": "last"})
	wantErr(t, "last", st, body, 409, "last_identity")
	if n := auditRows(t, a, "user.unlink"); n != 1 {
		t.Fatalf("audit rows after failures = %d, want 1", n)
	}

	st, body = call(t, support, srv, "POST", "/api/users/"+itoa(alice)+"/disconnect", map[string]string{})
	wantErr(t, "disconnect no reason", st, body, 400, "reason_required")
	if st, body := call(t, support, srv, "POST", "/api/users/"+itoa(alice)+"/disconnect", map[string]string{"reason": "abuse"}); st != 204 {
		t.Fatalf("disconnect: %d %v", st, body)
	}
	if d := a.Transport.Disconnected(); len(d) != 1 || d[0] != alice {
		t.Fatalf("disconnected %v", d)
	}
	if n := auditRows(t, a, "user.disconnect"); n != 1 {
		t.Fatalf("disconnect audit rows = %d", n)
	}
	st, body = call(t, support, srv, "POST", "/api/users/999999/disconnect", map[string]string{"reason": "x"})
	wantErr(t, "unknown user", st, body, 404, "not_found")
}

func TestRoleChangeKillsSessions(t *testing.T) {
	a, _, srv := writeEnv(t)
	owner := loginAs(t, a, srv, "owner", RoleOwner)
	op := loginAs(t, a, srv, "op", RoleOperator)
	if st, body := call(t, op, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatalf("op me: %d %v", st, body)
	}
	id := adminID(t, a, "op@brand.example")
	if st, body := call(t, owner, srv, "PATCH", "/api/admins/"+itoa(id), map[string]any{"role": "viewer"}); st != 200 {
		t.Fatalf("demote: %d %v", st, body)
	}
	st, body := call(t, op, srv, "GET", "/api/me", nil)
	wantErr(t, "old cookie", st, body, 401, "unauthorized")
	if n := auditRows(t, a, "admin.update"); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	st, body = call(t, owner, srv, "PATCH", "/api/admins/"+itoa(id), map[string]any{"role": "wizard"})
	wantErr(t, "bad role", st, body, 400, "bad_request")
	st, body = call(t, owner, srv, "PATCH", "/api/admins/999999", map[string]any{"role": "viewer"})
	wantErr(t, "missing", st, body, 404, "not_found")
	st, body = call(t, op, srv, "PATCH", "/api/admins/"+itoa(id), map[string]any{"role": "owner"})
	wantErr(t, "not logged in", st, body, 401, "unauthorized")

}

func TestLastOwner(t *testing.T) {
	a, _, srv := writeEnv(t)
	owner := loginAs(t, a, srv, "owner", RoleOwner)
	me := adminID(t, a, "owner@brand.example")
	st, body := call(t, owner, srv, "PATCH", "/api/admins/"+itoa(me), map[string]any{"role": "operator"})
	wantErr(t, "demote", st, body, 409, "last_owner")
	st, body = call(t, owner, srv, "PATCH", "/api/admins/"+itoa(me), map[string]any{"disabled": true})
	wantErr(t, "disable", st, body, 409, "last_owner")

	// With a second owner, still not yourself; the other can be demoted until one is left.
	other := loginAs(t, a, srv, "other", RoleOwner)
	otherID := adminID(t, a, "other@brand.example")
	st, body = call(t, owner, srv, "PATCH", "/api/admins/"+itoa(me), map[string]any{"role": "operator"})
	wantErr(t, "self demote", st, body, 409, "self")
	st, body = call(t, owner, srv, "PATCH", "/api/admins/"+itoa(me), map[string]any{"disabled": true})
	wantErr(t, "self disable", st, body, 409, "self")
	if st, body := call(t, owner, srv, "PATCH", "/api/admins/"+itoa(otherID), map[string]any{"disabled": true}); st != 200 {
		t.Fatalf("disable other: %d %v", st, body)
	}
	st, body = call(t, other, srv, "GET", "/api/me", nil)
	wantErr(t, "disabled owner", st, body, 401, "unauthorized")
	if n := auditRows(t, a, "admin.update"); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
}

func TestAuditHasNoSecrets(t *testing.T) {
	a, _, srv := writeEnv(t)
	owner := loginAs(t, a, srv, "owner", RoleOwner)
	loginAs(t, a, srv, "op", RoleOperator)
	id := adminID(t, a, "op@brand.example")
	path := "/api/admins/" + itoa(id)
	st, body := call(t, owner, srv, "PATCH", path, map[string]any{"reset_totp": true})
	if st != 200 || body["password"] != nil {
		t.Fatalf("reset_totp: %d %v", st, body)
	}
	var secret *string
	var last int64
	if err := a.Pool.QueryRow(context.Background(), `SELECT encode(totp_secret, 'hex'), totp_last_step FROM admin_users WHERE id = $1`, id).Scan(&secret, &last); err != nil {
		t.Fatal(err)
	}
	if secret != nil || last != 0 {
		t.Fatalf("totp not reset: %v %d", secret, last)
	}
	st, body = call(t, owner, srv, "PATCH", path, map[string]any{"reset_password": true})
	pw, _ := body["password"].(string)
	if st != 200 || len(pw) != passwordLen {
		t.Fatalf("reset_password: %d %v", st, body)
	}
	rows, err := a.Pool.Query(context.Background(), `SELECT coalesce(before::text, '') || coalesce(after::text, '') FROM admin_audit`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		n++
		for _, bad := range []string{"password_hash", "totp", pw, "argon2"} {
			if strings.Contains(s, bad) {
				t.Fatalf("audit row leaks %q: %s", bad, s)
			}
		}
	}
	if n == 0 {
		t.Fatal("no audit rows")
	}
	// The new password logs in (and the user enrols TOTP again).
	secrets.Delete(secretKey{srv, "op@brand.example"})
	login(t, srv, "op@brand.example", pw)
}

func TestSideEffectsSurviveCancel(t *testing.T) {
	a, adm, _ := writeEnv(t)
	alice := a.User("alice")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adm.publishConfig(ctx)
	if n := len(a.Pushes(gameconfig.Channel)); n != 1 {
		t.Fatalf("config pushes = %d, want 1", n)
	}
	if err := adm.kick(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if d := a.Transport.Disconnected(); len(d) != 1 || d[0] != alice {
		t.Fatalf("disconnected %v", d)
	}
}
