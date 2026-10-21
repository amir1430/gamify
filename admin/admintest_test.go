package admin

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
)

const testOrigin = "https://admin.brand.example"

// testKey is a fixed AES-256 key for sealing TOTP secrets in tests.
var testKey = bytes.Repeat([]byte{7}, 32)

// newAdmin registers the admin on a fresh App and serves its handler over TLS (the cookie is
// Secure). opts change the Config before Register.
func newAdmin(t *testing.T, opts ...func(*Config)) (*apptest.App, *Admin, *httptest.Server) {
	t.Helper()
	a := apptest.New(t)
	cfg := Config{Addr: "127.0.0.1:0", Origin: testOrigin, TOTPKey: testKey, Brand: Brand{Name: "Brand", Accent: "#7c3aed"}}
	for _, f := range opts {
		f(&cfg)
	}
	adm, err := Register(context.Background(), a.App, cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(adm.Handler())
	t.Cleanup(srv.Close)
	return a, adm, srv
}

// addUser creates a console user and returns their one-time password.
func addUser(t *testing.T, a *apptest.App, email string, role Role) string {
	t.Helper()
	pw, err := CreateUser(context.Background(), a.Pool, email, role)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

// client is a browser: TLS trusted, its own cookie jar.
func client(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := *srv.Client() // a copy: srv.Client() is one shared client
	c.Jar = jar
	return &c
}

// call sends a request the way the UI does (X-Admin and Origin on writes) and decodes the JSON
// response into a map (nil for an empty or non-JSON body).
func call(t *testing.T, c *http.Client, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return send(t, c, srv, method, path, body, map[string]string{"X-Admin": "1", "Origin": testOrigin})
}

func send(t *testing.T, c *http.Client, srv *httptest.Server, method, path string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return resp.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %q", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, out
}

// wantErr checks a JSON error response.
func wantErr(t *testing.T, what string, status int, body map[string]any, wantStatus int, code string) {
	t.Helper()
	if status != wantStatus || body["code"] != code {
		t.Fatalf("%s: %d %v, want %d %s", what, status, body, wantStatus, code)
	}
}

// secrets remembers each enrolled user's TOTP secret and the last step a helper used, so login
// can answer the TOTP prompt the way the user's authenticator app would.
var secrets sync.Map // secretKey → *enrolled

// secretKey is per server, not per URL: a closed test server's port can be reused.
type secretKey struct {
	srv   *httptest.Server
	email string
}

type enrolled struct {
	secret []byte
	last   int64
}

// nextCode is a valid code the server hasn't seen yet: the current step, or the next one when
// the current step was used already (verifyTOTP accepts one step of drift).
func (e *enrolled) nextCode(t *testing.T) string {
	t.Helper()
	step := max(time.Now().Unix()/totpPeriod, e.last+1)
	if step > time.Now().Unix()/totpPeriod+1 {
		t.Fatal("test logged in too often within one TOTP step")
	}
	e.last = step
	return totpCode(e.secret, step)
}

// enrolFrom reads the secret out of an otpauth URI.
func enrolFrom(t *testing.T, uri string) *enrolled {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	s, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(u.Query().Get("secret"))
	if err != nil {
		t.Fatal(err)
	}
	return &enrolled{secret: s}
}

// login logs a console user in, enrolling TOTP through the API on their first login, and returns a
// client holding an mfa session.
func login(t *testing.T, srv *httptest.Server, email, pw string) *http.Client {
	t.Helper()
	c := client(t, srv)
	key := secretKey{srv, email}
	if v, ok := secrets.Load(key); ok {
		e := v.(*enrolled)
		st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": email, "password": pw, "totp": e.nextCode(t)})
		if st != http.StatusOK {
			t.Fatalf("login %s: %d %v", email, st, body)
		}
		return c
	}
	st, body := call(t, c, srv, "POST", "/api/login", map[string]string{"email": email, "password": pw})
	if st != http.StatusOK || body["enrol"] != true {
		t.Fatalf("login %s: %d %v", email, st, body)
	}
	e := enrolFrom(t, body["otpauth_uri"].(string))
	if st, body := call(t, c, srv, "POST", "/api/totp/enrol", map[string]string{"code": e.nextCode(t)}); st != http.StatusOK {
		t.Fatalf("enrol %s: %d %v", email, st, body)
	}
	secrets.Store(key, e)
	return c
}
