// Package admintest drives a console over HTTP the way its UI does, so a game's admin views and
// actions can be tested from their own package.
package admintest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
)

const origin = "https://admin.brand.example"

// Config is an admin.Config for tests; change it before admin.Register.
func Config() admin.Config {
	return admin.Config{Addr: "127.0.0.1:0", Origin: origin, TOTPKey: bytes.Repeat([]byte{7}, 32)}
}

// Console is a console served over TLS for the test.
type Console struct {
	t   testing.TB
	a   *apptest.App
	srv *httptest.Server
	n   int
}

// Start serves adm for the test.
func Start(t testing.TB, a *apptest.App, adm *admin.Admin) *Console {
	t.Helper()
	srv := httptest.NewTLSServer(adm.Handler())
	t.Cleanup(srv.Close)
	return &Console{t: t, a: a, srv: srv}
}

// Client is a logged-in console user.
type Client struct {
	t    testing.TB
	c    *http.Client
	srv  *httptest.Server
	Role admin.Role
}

// Login creates a console user with role, enrols TOTP and logs in.
func (cs *Console) Login(role admin.Role) *Client {
	t := cs.t
	t.Helper()
	cs.n++
	email := fmt.Sprintf("%s%d@brand.example", role, cs.n)
	pw, err := admin.CreateUser(context.Background(), cs.a.Pool, email, role)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	hc := *cs.srv.Client()
	hc.Jar = jar
	cl := &Client{t: t, c: &hc, srv: cs.srv, Role: role}
	st, body := cl.Do("POST", "/api/login", map[string]string{"email": email, "password": pw})
	if st != http.StatusOK || body["enrol"] != true {
		t.Fatalf("login %s: %d %v", email, st, body)
	}
	u, err := url.Parse(body["otpauth_uri"].(string))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(u.Query().Get("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if st, body := cl.Do("POST", "/api/totp/enrol", map[string]string{"code": code(secret, time.Now().Unix()/30)}); st != http.StatusOK {
		t.Fatalf("enrol %s: %d %v", email, st, body)
	}
	return cl
}

// code is RFC 6238's SHA-1, 6-digit code of a 30 s step.
func code(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// Do sends a request and decodes a JSON response object (nil for an empty body).
func (cl *Client) Do(method, path string, body any) (int, map[string]any) {
	t := cl.t
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, cl.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Admin", "1")
	req.Header.Set("Origin", origin)
	resp, err := cl.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return resp.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %q", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, out
}
