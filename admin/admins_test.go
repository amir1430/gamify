package admin

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/amir1430/gamify/apptest"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func adminID(t *testing.T, a *apptest.App, email string) int64 {
	t.Helper()
	var id int64
	if err := a.Pool.QueryRow(context.Background(), `SELECT id FROM admin_users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCreateAdmin(t *testing.T) {
	a, _, srv := writeEnv(t)
	owner := loginAs(t, a, srv, "owner", RoleOwner)
	op := loginAs(t, a, srv, "op", RoleOperator)

	st, body := call(t, op, srv, "POST", "/api/admins", map[string]string{"email": "x@brand.example", "role": "viewer"})
	wantErr(t, "operator", st, body, 403, "forbidden")
	st, body = call(t, owner, srv, "POST", "/api/admins", map[string]string{"email": "New@Brand.example ", "role": "viewer"})
	pw, _ := body["password"].(string)
	if st != 200 || len(pw) != passwordLen {
		t.Fatalf("create: %d %v", st, body)
	}
	var adminIDCol *int64
	if err := a.Pool.QueryRow(context.Background(), `SELECT admin_id FROM admin_audit WHERE action = 'admin.create' AND after->>'email' = 'new@brand.example'`).Scan(&adminIDCol); err != nil {
		t.Fatal(err)
	}
	if adminIDCol == nil || *adminIDCol != adminID(t, a, "owner@brand.example") {
		t.Fatalf("admin.create admin_id = %v", adminIDCol)
	}
	login(t, srv, "new@brand.example", pw)

	st, body = call(t, owner, srv, "POST", "/api/admins", map[string]string{"email": "NEW@brand.example", "role": "viewer"})
	wantErr(t, "dup", st, body, 409, "email_taken")
	st, body = call(t, owner, srv, "POST", "/api/admins", map[string]string{"email": "nope", "role": "viewer"})
	wantErr(t, "bad email", st, body, 400, "bad_request")
	st, body = call(t, owner, srv, "POST", "/api/admins", map[string]string{"email": "y@brand.example", "role": "king"})
	wantErr(t, "bad role", st, body, 400, "bad_request")

	if _, err := CreateUser(context.Background(), a.Pool, "op@brand.example", RoleViewer); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("CreateUser dup: %v", err)
	}

	st, list := call(t, owner, srv, "GET", "/api/admins", nil)
	items, _ := list["items"].([]any)
	if st != 200 || len(items) != 3 {
		t.Fatalf("list: %d %v", st, list)
	}
	first := items[0].(map[string]any)
	for _, k := range []string{"id", "email", "role", "telegram_id", "disabled", "totp_enrolled", "created_at"} {
		if _, ok := first[k]; !ok {
			t.Errorf("list item lacks %s: %v", k, first)
		}
	}
	if len(first) != 7 {
		t.Errorf("list item has extra fields: %v", first)
	}
	if first["totp_enrolled"] != true {
		t.Errorf("owner totp_enrolled = %v", first["totp_enrolled"])
	}
}

func TestMePassword(t *testing.T) {
	a, _, srv := writeEnv(t)
	pw := addUser(t, a, "op@brand.example", RoleOperator)
	c1 := login(t, srv, "op@brand.example", pw)
	c2 := login(t, srv, "op@brand.example", pw)
	call2 := func(body map[string]string) (int, map[string]any) {
		return call(t, c1, srv, "POST", "/api/me/password", body)
	}

	st, body := call2(map[string]string{"current": "wrong", "new": "a-long-enough-password"})
	wantErr(t, "wrong", st, body, 400, "invalid_password")
	st, body = call2(map[string]string{"current": pw, "new": "short"})
	wantErr(t, "weak", st, body, 400, "weak_password")
	if n := auditRows(t, a, "admin.password"); n != 0 {
		t.Fatalf("audit rows after failures = %d", n)
	}
	if st, body := call2(map[string]string{"current": pw, "new": "a-long-enough-password"}); st != 204 {
		t.Fatalf("change: %d %v", st, body)
	}
	if st, _ := call(t, c1, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatalf("current session: %d", st)
	}
	st, body = call(t, c2, srv, "GET", "/api/me", nil)
	wantErr(t, "other session", st, body, 401, "unauthorized")
	var before, after *string
	if err := a.Pool.QueryRow(context.Background(), `SELECT before::text, after::text FROM admin_audit WHERE action = 'admin.password'`).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if before != nil || after != nil {
		t.Fatalf("admin.password has values: %v %v", before, after)
	}
	var hash string
	if err := a.Pool.QueryRow(context.Background(), `SELECT password_hash FROM admin_users WHERE email = 'op@brand.example'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !checkPassword(hash, "a-long-enough-password") {
		t.Fatal("new password not stored")
	}
}

func TestPatchNoOpAndDisable(t *testing.T) {
	a, _, srv := writeEnv(t)
	owner := loginAs(t, a, srv, "owner", RoleOwner)
	op := loginAs(t, a, srv, "op", RoleOperator)
	path := "/api/admins/" + itoa(adminID(t, a, "op@brand.example"))
	if st, body := call(t, owner, srv, "PATCH", path, map[string]any{"role": "operator"}); st != 200 {
		t.Fatalf("no-op: %d %v", st, body)
	}
	if n := auditRows(t, a, "admin.update"); n != 0 {
		t.Fatalf("no-op wrote %d audit rows", n)
	}
	if st, _ := call(t, op, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatal("no-op killed the session")
	}
	st, body := call(t, owner, srv, "PATCH", path, map[string]any{})
	wantErr(t, "empty", st, body, 400, "bad_request")
	if st, body := call(t, owner, srv, "PATCH", path, map[string]any{"disabled": true}); st != 200 {
		t.Fatalf("disable: %d %v", st, body)
	}
	if st, _ := call(t, op, srv, "GET", "/api/me", nil); st != 401 {
		t.Fatalf("disabled user's session: %d", st)
	}
}

func TestMePasswordBusy(t *testing.T) {
	a, adm, srv := writeEnv(t)
	pw := addUser(t, a, "op@brand.example", RoleOperator)
	c := login(t, srv, "op@brand.example", pw)
	for range cap(adm.logins) {
		adm.logins <- struct{}{}
	}
	st, body := call(t, c, srv, "POST", "/api/me/password", map[string]string{"current": pw, "new": "a-long-enough-password"})
	wantErr(t, "busy", st, body, 429, "busy")
}
