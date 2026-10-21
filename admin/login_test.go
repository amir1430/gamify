package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
)

func auditCount(t *testing.T, a *apptest.App, action, target string) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM admin_audit WHERE action = $1 AND target = $2`, action, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exec(t *testing.T, a *apptest.App, sql string, args ...any) {
	t.Helper()
	if _, err := a.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestLoginFlow(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := client(t, srv)

	st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": "wrong"})
	wantErr(t, "wrong password", st, body, 401, "invalid_login")
	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "nobody@brand.example", "password": pw})
	wantErr(t, "unknown email", st, body, 401, "invalid_login")
	if auditCount(t, a, "login.fail", "email:owner@brand.example") != 1 || auditCount(t, a, "login.fail", "email:nobody@brand.example") != 1 {
		t.Fatal("failed logins not audited")
	}

	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "Owner@Brand.Example", "password": pw})
	if st != 200 || body["enrol"] != true {
		t.Fatalf("first login: %d %v", st, body)
	}
	uri := body["otpauth_uri"].(string)
	if !strings.HasPrefix(uri, "otpauth://totp/Brand:owner%40brand.example?") || !strings.Contains(uri, "&issuer=Brand&") {
		t.Fatalf("uri %q", uri)
	}
	st, body = call(t, c, srv, "GET", "/api/me", nil)
	wantErr(t, "me before enrolment", st, body, 401, "mfa_required")

	st, body = call(t, c, srv, "POST", "/api/totp/enrol", map[string]string{"code": "000000x"})
	wantErr(t, "bad enrol code", st, body, 400, "invalid_code")
	e := enrolFrom(t, uri)
	if st, body := call(t, c, srv, "POST", "/api/totp/enrol", map[string]string{"code": e.nextCode(t)}); st != 200 {
		t.Fatalf("enrol: %d %v", st, body)
	}
	var id int64
	if err := a.Pool.QueryRow(context.Background(), `SELECT id FROM admin_users WHERE email = 'owner@brand.example'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	target := "admin:" + strconv.FormatInt(id, 10)
	if auditCount(t, a, "login.ok", target) != 1 || auditCount(t, a, "totp.enrol", target) != 1 {
		t.Fatal("login or enrolment not audited")
	}

	st, body = call(t, c, srv, "GET", "/api/me", nil)
	if st != 200 {
		t.Fatalf("me: %d %v", st, body)
	}
	user := body["user"].(map[string]any)
	if user["email"] != "owner@brand.example" || user["role"] != "owner" || user["id"] != float64(id) {
		t.Fatalf("user %v", user)
	}
	var perms []string
	for _, p := range body["permissions"].([]any) {
		perms = append(perms, p.(string))
	}
	if want := []string{"adjust", "admins", "approve", "identity", "limits", "view"}; !slices.Equal(perms, want) {
		t.Fatalf("permissions %v, want %v", perms, want)
	}
	brand := body["brand"].(map[string]any)
	if brand["name"] != "Brand" || brand["accent"] != "#7c3aed" || brand["logo"] != nil {
		t.Fatalf("brand %v", brand)
	}
	cur := body["currencies"].([]any)
	if len(cur) != 2 || cur[0].(map[string]any)["code"] != "usd" || cur[1].(map[string]any)["code"] != "tickets" || cur[0].(map[string]any)["decimals"] != float64(0) {
		t.Fatalf("currencies %v", cur)
	}

	if st, body := call(t, c, srv, "POST", "/api/logout", nil); st != 200 {
		t.Fatalf("logout: %d %v", st, body)
	}
	if auditCount(t, a, "logout", target) != 1 {
		t.Fatal("logout not audited")
	}
	st, body = call(t, c, srv, "GET", "/api/me", nil)
	wantErr(t, "me after logout", st, body, 401, "unauthorized")
	var sessions int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("%d sessions left (%v)", sessions, err)
	}
}

func TestSessionCookie(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	req, _ := http.NewRequest("POST", srv.URL+"/api/login",
		strings.NewReader(`{"email":"owner@brand.example","password":"`+pw+`"}`))
	req.Header.Set("X-Admin", "1")
	req.Header.Set("Origin", testOrigin)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	cs := resp.Cookies()
	if len(cs) != 1 {
		t.Fatalf("cookies %v", cs)
	}
	ck := cs[0]
	if ck.Name != "gamify_admin" || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || len(ck.Value) != 43 {
		t.Fatalf("cookie %+v", ck)
	}
	// Stored as its SHA-256, never the token itself.
	var stored []byte
	var ua string
	if err := a.Pool.QueryRow(context.Background(), `SELECT hash, user_agent FROM admin_sessions`).Scan(&stored, &ua); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(ck.Value)) || len(stored) != 32 || ua == "" {
		t.Fatalf("stored hash %x, user agent %q", stored, ua)
	}
}

func TestTOTPLogin(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "op@brand.example", RoleOperator)
	login(t, srv, "op@brand.example", pw) // enrols
	v, _ := secrets.Load(secretKey{srv, "op@brand.example"})
	e := v.(*enrolled)
	c := client(t, srv)

	st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": "op@brand.example", "password": pw})
	wantErr(t, "no totp", st, body, 401, "totp_required")
	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "op@brand.example", "password": pw, "totp": "123456x"})
	wantErr(t, "bad totp", st, body, 401, "invalid_login")
	// The enrolment's own code is spent: replaying it is refused.
	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "op@brand.example", "password": pw, "totp": totpCode(e.secret, e.last)})
	wantErr(t, "replayed totp", st, body, 401, "invalid_login")
	code := e.nextCode(t)
	if st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": "op@brand.example", "password": pw, "totp": code}); st != 200 || body["enrol"] != false {
		t.Fatalf("login: %d %v", st, body)
	}
	st, body = call(t, client(t, srv), srv, "POST", "/api/login", map[string]string{"email": "op@brand.example", "password": pw, "totp": code})
	wantErr(t, "same code twice", st, body, 401, "invalid_login")
	if st, body := call(t, c, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatalf("me: %d %v", st, body)
	}
	// A wrong TOTP counts as a failure; a missing one doesn't (it's the UI's first step).
	var fails int
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM admin_audit WHERE action = 'login.fail' AND reason = 'totp'`).Scan(&fails); err != nil || fails != 3 {
		t.Fatalf("%d totp failures (%v)", fails, err)
	}
}

func TestLockout(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := client(t, srv)
	try := func(pw string) (int, map[string]any) {
		return call(t, c, srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": pw})
	}
	for range 5 {
		st, body := try("wrong")
		wantErr(t, "wrong password", st, body, 401, "invalid_login")
	}
	st, body := try(pw)
	wantErr(t, "locked", st, body, 429, "locked")
	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "OWNER@brand.example ", "password": pw})
	wantErr(t, "locked, other case", st, body, 429, "locked")

	exec(t, a, `UPDATE admin_audit SET created_at = created_at - interval '16 minutes'`)
	if st, body := try(pw); st != 200 {
		t.Fatalf("after 16 min: %d %v", st, body)
	}
	// A successful login starts the count again.
	for range 4 {
		try("wrong")
	}
	if st, body := try(pw); st != 200 {
		t.Fatalf("4 failures since the last login: %d %v", st, body)
	}

	// Unknown emails lock too, so the response doesn't reveal which exist.
	for range 5 {
		call(t, c, srv, "POST", "/api/login", map[string]string{"email": "nobody@brand.example", "password": "x"})
	}
	st, body = call(t, c, srv, "POST", "/api/login", map[string]string{"email": "nobody@brand.example", "password": "x"})
	wantErr(t, "unknown email locked", st, body, 429, "locked")
}

func TestSessionTimeouts(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := login(t, srv, "owner@brand.example", pw)
	if st, _ := call(t, c, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatal(st)
	}
	exec(t, a, `UPDATE admin_sessions SET seen_at = now() - interval '31 minutes'`)
	st, body := call(t, c, srv, "GET", "/api/me", nil)
	wantErr(t, "idle", st, body, 401, "unauthorized")

	c = login(t, srv, "owner@brand.example", pw)
	exec(t, a, `UPDATE admin_sessions SET created_at = now() - interval '13 hours', seen_at = now()`)
	st, body = call(t, c, srv, "GET", "/api/me", nil)
	wantErr(t, "absolute", st, body, 401, "unauthorized")
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d expired sessions kept (%v)", n, err)
	}
}

func TestSessionSeenAt(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := login(t, srv, "owner@brand.example", pw)
	exec(t, a, `UPDATE admin_sessions SET seen_at = now() - interval '30 seconds'`)
	call(t, c, srv, "GET", "/api/me", nil)
	var age time.Duration
	read := func() {
		var secs float64
		if err := a.Pool.QueryRow(context.Background(), `SELECT extract(epoch FROM now() - seen_at) FROM admin_sessions`).Scan(&secs); err != nil {
			t.Fatal(err)
		}
		age = time.Duration(secs * float64(time.Second))
	}
	if read(); age < 29*time.Second {
		t.Fatalf("seen_at refreshed after %s", age)
	}
	exec(t, a, `UPDATE admin_sessions SET seen_at = now() - interval '5 minutes'`)
	call(t, c, srv, "GET", "/api/me", nil)
	if read(); age > 10*time.Second {
		t.Fatalf("seen_at not refreshed: %s old", age)
	}
}

func TestDisabledUser(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := login(t, srv, "owner@brand.example", pw)
	exec(t, a, `UPDATE admin_users SET disabled_at = now()`)
	st, body := call(t, c, srv, "GET", "/api/me", nil)
	wantErr(t, "disabled session", st, body, 401, "unauthorized")
	st, body = call(t, client(t, srv), srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": pw})
	wantErr(t, "disabled login", st, body, 401, "invalid_login")
}

func telegramFields(token string, id int64, at time.Time) map[string]string {
	f := map[string]string{"id": strconv.FormatInt(id, 10), "first_name": "Ann", "auth_date": strconv.FormatInt(at.Unix(), 10)}
	var pairs []string
	for k, v := range f {
		pairs = append(pairs, k+"="+v)
	}
	slices.Sort(pairs)
	secret := sha256.Sum256([]byte(token))
	m := hmac.New(sha256.New, secret[:])
	m.Write([]byte(strings.Join(pairs, "\n")))
	f["hash"] = hex.EncodeToString(m.Sum(nil))
	return f
}

func TestTelegramLogin(t *testing.T) {
	_, _, plain := newAdmin(t)
	st, _ := call(t, client(t, plain), plain, "POST", "/api/login/telegram", map[string]any{"fields": map[string]string{}})
	if st != 404 {
		t.Fatalf("telegram login without a bot token: %d", st)
	}

	const token = "123:abc"
	a, _, srv := newAdmin(t, func(c *Config) { c.TelegramBotToken = token })
	addUser(t, a, "owner@brand.example", RoleOwner)
	exec(t, a, `UPDATE admin_users SET telegram_id = 4242`)
	c := client(t, srv)

	bad := telegramFields(token, 4242, time.Now())
	bad["first_name"] = "Mallory"
	st, body := call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": bad})
	wantErr(t, "bad signature", st, body, 401, "invalid_login")
	st, body = call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now().Add(-25*time.Hour))})
	wantErr(t, "too old", st, body, 401, "invalid_login")
	st, body = call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 99, time.Now())})
	wantErr(t, "unlinked telegram", st, body, 401, "invalid_login")

	st, body = call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now())})
	if st != 200 || body["enrol"] != true {
		t.Fatalf("telegram login: %d %v", st, body)
	}
	e := enrolFrom(t, body["otpauth_uri"].(string))
	if st, body := call(t, c, srv, "POST", "/api/totp/enrol", map[string]string{"code": e.nextCode(t)}); st != 200 {
		t.Fatalf("enrol: %d %v", st, body)
	}
	c2 := client(t, srv)
	st, body = call(t, c2, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now())})
	wantErr(t, "no totp", st, body, 401, "totp_required")
	if st, body := call(t, c2, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now()), "totp": e.nextCode(t)}); st != 200 {
		t.Fatalf("telegram login with totp: %d %v", st, body)
	}
	if st, _ := call(t, c2, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatal(st)
	}

	for range 5 {
		call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now()), "totp": "000000"})
	}
	st, body = call(t, c, srv, "POST", "/api/login/telegram", map[string]any{"fields": telegramFields(token, 4242, time.Now()), "totp": "000001"})
	wantErr(t, "telegram locked", st, body, 429, "locked")
	if auditCount(t, a, "login.fail", "telegram:4242") < 5 {
		t.Fatal("telegram failures not audited")
	}
}

func TestBrandLogo(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for _, tc := range []struct {
		logo []byte
		typ  string
	}{
		{png, "image/png"},
		{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "image/svg+xml"},
		{[]byte(`<?xml version="1.0"?><svg></svg>`), "image/svg+xml"},
	} {
		a, _, srv := newAdmin(t, func(c *Config) { c.Brand.Logo = tc.logo })
		resp, err := srv.Client().Get(srv.URL + "/api/brand/logo")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != tc.typ || !bytes.Equal(got, tc.logo) {
			t.Fatalf("logo: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		pw := addUser(t, a, "owner@brand.example", RoleOwner)
		_, body := call(t, login(t, srv, "owner@brand.example", pw), srv, "GET", "/api/me", nil)
		if body["brand"].(map[string]any)["logo"] != "/api/brand/logo" {
			t.Fatalf("me brand %v", body["brand"])
		}
	}
	_, _, srv := newAdmin(t)
	resp, err := srv.Client().Get(srv.URL + "/api/brand/logo")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("no logo: %d", resp.StatusCode)
	}
}

func TestLedgerDecimals(t *testing.T) {
	cfg := apptest.Currencies[0]
	cfg.Decimals = 2
	b := apptest.New(t, apptest.WithCurrencies(cfg))
	if d := b.Ledger.Decimals("usd"); d != 2 {
		t.Fatalf("Decimals = %d", d)
	}
	if d := b.Ledger.Decimals("nope"); d != 0 {
		t.Fatalf("unknown currency Decimals = %d", d)
	}
}

func TestCreateUserAudits(t *testing.T) {
	a := apptest.New(t)
	addUser(t, a, "Owner@Brand.Example", RoleOwner)
	var adminID *int64
	var target, after string
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT admin_id, target, after::text FROM admin_audit WHERE action = 'admin.create'`).Scan(&adminID, &target, &after); err != nil {
		t.Fatal(err)
	}
	if adminID != nil || !strings.HasPrefix(target, "admin:") || after != `{"role": "owner", "email": "owner@brand.example"}` {
		t.Fatalf("audit %v %q %q", adminID, target, after)
	}
}

func TestTOTPConcurrentReplay(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	login(t, srv, "owner@brand.example", pw)
	v, _ := secrets.Load(secretKey{srv, "owner@brand.example"})
	code := v.(*enrolled).nextCode(t)
	const n = 4
	got := make(chan int, n)
	for range n {
		go func() {
			got <- postLogin(srv, `{"email":"owner@brand.example","password":"`+pw+`","totp":"`+code+`"}`)
		}()
	}
	ok := 0
	for range n {
		if <-got == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d of %d concurrent logins with one code succeeded", ok, n)
	}
}

func TestLockoutParallel(t *testing.T) {
	a, _, srv := newAdmin(t)
	addUser(t, a, "owner@brand.example", RoleOwner)
	const n = 20
	got := make(chan int, n)
	for range n {
		go func() { got <- postLogin(srv, `{"email":"owner@brand.example","password":"wrong"}`) }()
	}
	codes := map[int]int{}
	for range n {
		codes[<-got]++
	}
	if codes[401] != maxLoginFails || codes[429] != n-maxLoginFails {
		t.Fatalf("statuses %v: want %d × 401 and %d × 429", codes, maxLoginFails, n-maxLoginFails)
	}
	var evaluated, locked int
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE reason = 'password'), count(*) FILTER (WHERE reason = 'locked')
		 FROM admin_audit WHERE action = 'login.fail' AND target = 'email:owner@brand.example'`).Scan(&evaluated, &locked); err != nil {
		t.Fatal(err)
	}
	if evaluated != maxLoginFails || locked != n-maxLoginFails {
		t.Fatalf("%d passwords checked, %d locked", evaluated, locked)
	}
}

func TestEnrolRotatesSession(t *testing.T) {
	a, _, srv := newAdmin(t)
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	c := client(t, srv)
	st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": pw})
	if st != 200 || body["enrol"] != true {
		t.Fatalf("login: %d %v", st, body)
	}
	u, _ := url.Parse(srv.URL)
	before := c.Jar.Cookies(u)
	e := enrolFrom(t, body["otpauth_uri"].(string))
	if st, body := call(t, c, srv, "POST", "/api/totp/enrol", map[string]string{"code": e.nextCode(t)}); st != 200 {
		t.Fatalf("enrol: %d %v", st, body)
	}
	after := c.Jar.Cookies(u)
	if len(before) != 1 || len(after) != 1 || before[0].Value == after[0].Value {
		t.Fatalf("cookie not rotated: %v → %v", before, after)
	}
	old := client(t, srv)
	old.Jar.SetCookies(u, before)
	st, body = call(t, old, srv, "GET", "/api/me", nil)
	wantErr(t, "pre-enrol cookie", st, body, 401, "unauthorized")
	if st, body := call(t, c, srv, "GET", "/api/me", nil); st != 200 {
		t.Fatalf("new cookie: %d %v", st, body)
	}
	var sessions int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_sessions`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("%d sessions (%v)", sessions, err)
	}
}

// postLogin posts a login from its own goroutine, retrying while the server is busy (the
// concurrency cap), and returns the final status (0 on a transport error).
func postLogin(srv *httptest.Server, body string) int {
	for {
		req, _ := http.NewRequest("POST", srv.URL+"/api/login", strings.NewReader(body))
		req.Header.Set("X-Admin", "1")
		req.Header.Set("Origin", testOrigin)
		resp, err := srv.Client().Do(req)
		if err != nil {
			return 0
		}
		var e struct{ Code string }
		json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests || e.Code != "busy" {
			return resp.StatusCode
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLoginBusy(t *testing.T) {
	a, adm, srv := newAdmin(t, func(c *Config) { c.TelegramBotToken = "123:abc" })
	pw := addUser(t, a, "owner@brand.example", RoleOwner)
	for range cap(adm.logins) {
		adm.logins <- struct{}{}
	}
	for _, path := range []string{"/api/login", "/api/login/telegram"} {
		req, _ := http.NewRequest("POST", srv.URL+path,
			strings.NewReader(`{"email":"owner@brand.example","password":"`+pw+`","fields":{}}`))
		req.Header.Set("X-Admin", "1")
		req.Header.Set("Origin", testOrigin)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var e struct{ Code string }
		json.NewDecoder(resp.Body).Decode(&e)
		resp.Body.Close()
		if resp.StatusCode != 429 || e.Code != "busy" || resp.Header.Get("Retry-After") != "1" {
			t.Fatalf("%s while full: %d %q Retry-After %q", path, resp.StatusCode, e.Code, resp.Header.Get("Retry-After"))
		}
	}
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit WHERE action LIKE 'login.%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d audit rows for busy refusals (%v)", n, err)
	}
	for range cap(adm.logins) {
		<-adm.logins
	}
	if st, body := call(t, client(t, srv), srv, "POST", "/api/login", map[string]string{"email": "owner@brand.example", "password": pw}); st != 200 {
		t.Fatalf("after release: %d %v", st, body)
	}
	if cap(adm.logins) != 4 {
		t.Fatalf("cap %d", cap(adm.logins))
	}
}
