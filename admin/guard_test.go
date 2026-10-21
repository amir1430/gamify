package admin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/amir1430/gamify/apptest"
)

func TestCSRF(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := login(t, srv, "owner@brand.example", pw)
	for _, h := range []map[string]string{
		{"Origin": testOrigin},
		{"X-Admin": "1"},
		{"X-Admin": "1", "Origin": "https://evil.example"},
		{"X-Admin": "1", "Origin": testOrigin + "/"},
		{"X-Admin": "0", "Origin": testOrigin},
	} {
		st, body := send(t, c, srv, "POST", "/api/logout", nil, h)
		wantErr(t, "logout "+strings.Join([]string{h["X-Admin"], h["Origin"]}, " "), st, body, 403, "csrf")
		st, body = send(t, client(t, srv), srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": pw}, h)
		wantErr(t, "login", st, body, 403, "csrf")
	}
	// GET needs neither, and the session survived the refused logouts.
	if st, body := send(t, c, srv, "GET", "/api/me", nil, nil); st != 200 {
		t.Fatalf("GET: %d %v", st, body)
	}
}

func TestAllowIPs(t *testing.T) {
	_, _, srv := newAdmin(t, func(c *Config) { c.AllowIPs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")} })
	for _, path := range []string{"/api/me", "/api/brand/logo", "/"} {
		st, body := send(t, client(t, srv), srv, "GET", path, nil, map[string]string{"X-Forwarded-For": "10.1.2.3"})
		wantErr(t, path, st, body, 403, "ip_not_allowed")
	}
	_, _, srv = newAdmin(t, func(c *Config) {
		c.AllowIPs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("127.0.0.0/8")}
	})
	st, body := send(t, client(t, srv), srv, "GET", "/api/me", nil, nil)
	wantErr(t, "allowed ip", st, body, 401, "unauthorized")
}

func TestPermissionGuard(t *testing.T) {
	a, adm, srv := newAdmin(t)
	// A route only this test registers: needs PermAdmins.
	adm.handle("GET /api/test-admins", PermAdmins, func(w http.ResponseWriter, r *http.Request, s *session) error {
		writeJSON(w, http.StatusOK, map[string]int64{"admin": s.AdminID})
		return nil
	})
	adm.handle("GET /api/test-fail", PermView, func(w http.ResponseWriter, r *http.Request, s *session) error {
		return io.ErrUnexpectedEOF
	})
	viewer := login(t, srv, "viewer@brand.example", addUser(t, a, "viewer@brand.example", RoleViewer))
	owner := login(t, srv, "owner@brand.example", addUser(t, a, "owner@brand.example", RoleOwner))
	st, body := call(t, viewer, srv, "GET", "/api/test-admins", nil)
	wantErr(t, "viewer", st, body, 403, "forbidden")
	if st, body := call(t, owner, srv, "GET", "/api/test-admins", nil); st != 200 {
		t.Fatalf("owner: %d %v", st, body)
	}
	st, body = call(t, client(t, srv), srv, "GET", "/api/test-admins", nil)
	wantErr(t, "no session", st, body, 401, "unauthorized")
	st, body = call(t, owner, srv, "GET", "/api/test-fail", nil)
	wantErr(t, "handler error", st, body, 500, "internal")
}

func TestEveryRouteHasPerm(t *testing.T) {
	_, adm, _ := newAdmin(t, func(c *Config) { c.TelegramBotToken = "123:abc" })
	// Routes without a permission: public, enrolment, or any mfa session. Add a route here only
	// when it must be open to every console user.
	open := map[string]bool{
		"POST /api/login":          true,
		"POST /api/login/telegram": true,
		"POST /api/totp/enrol":     true,
		"POST /api/logout":         true,
		"POST /api/me/password":    true,
		"GET /api/me":              true,
		"GET /api/brand/logo":      true,
		"GET /api/views":           true, // lists only the views the role holds
		// Brand views: each handler checks the view's and the action's own permission.
		"GET /api/views/{name}":                        true,
		"GET /api/views/{name}/{id}":                   true,
		"POST /api/views/{name}/{id}/actions/{action}": true,
	}
	if len(adm.routes) == 0 {
		t.Fatal("no routes")
	}
	for pattern, rt := range adm.routes {
		if rt.perm == "" && !open[pattern] {
			t.Errorf("%s has no permission", pattern)
		}
		if rt.perm != "" && !Role(RoleOwner).Can(rt.perm) {
			t.Errorf("%s needs %q, which no role holds", pattern, rt.perm)
		}
	}
	for pattern := range open {
		if _, ok := adm.routes[pattern]; !ok {
			t.Errorf("%s not registered", pattern)
		}
	}
}

func TestRegisterValidates(t *testing.T) {
	a := apptest.New(t)
	ok := Config{Addr: ":0", Origin: testOrigin, TOTPKey: testKey}
	for name, f := range map[string]func(*Config){
		"no addr":         func(c *Config) { c.Addr = "" },
		"no origin":       func(c *Config) { c.Origin = "" },
		"relative origin": func(c *Config) { c.Origin = "admin.brand.example" },
		"ftp origin":      func(c *Config) { c.Origin = "ftp://admin.brand.example" },
		"origin path":     func(c *Config) { c.Origin = "https://admin.brand.example/x" },
		"16-byte key":     func(c *Config) { c.TOTPKey = testKey[:16] },
		"24-byte key":     func(c *Config) { c.TOTPKey = testKey[:24] },
		"no key":          func(c *Config) { c.TOTPKey = nil },
		"bad prefix":      func(c *Config) { c.AllowIPs = []netip.Prefix{{}} },
	} {
		cfg := ok
		f(&cfg)
		if _, err := Register(context.Background(), a.App, cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Register(context.Background(), a.App, ok); err != nil {
		t.Fatal(err)
	}
}

func TestUI(t *testing.T) {
	_, _, srv := newAdmin(t)
	resp, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte("<html")) {
		t.Fatalf("embedded UI: %d %q", resp.StatusCode, b)
	}

	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html>brand ui</html>")}}
	_, _, srv = newAdmin(t, func(c *Config) { c.UI = ui })
	resp, err = srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte("brand ui")) {
		t.Fatalf("Config.UI: %d %q", resp.StatusCode, b)
	}
}
