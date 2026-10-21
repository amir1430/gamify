package diceadmin_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/games/dice"
	"github.com/amir1430/gamify/games/dice/diceadmin"
)

const origin = "https://admin.brand.example"

func TestDiceBetsView(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	fair, err := fairness.Register(ctx, a.App, apptest.Sealer(t))
	if err != nil {
		t.Fatal(err)
	}
	s, err := dice.Register(ctx, a.App, fair)
	if err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admin.Config{Addr: "127.0.0.1:0", Origin: origin, TOTPKey: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if err := diceadmin.Register(adm, s); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(adm.Handler())
	t.Cleanup(srv.Close)

	alice, bob := a.User("alice"), a.User("bob")
	for i, u := range []int64{alice, alice, bob} {
		a.Fund(u, "usd", 1_000)
		if _, cerr := a.Call(u, "dice.bet", dice.BetRequest{Amount: 100, Target: 5000, Over: true, IdemKey: strconv.Itoa(i)}); cerr != nil {
			t.Fatal(cerr)
		}
	}

	pw, err := admin.CreateUser(ctx, a.Pool, "viewer@brand.example", admin.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, srv, "viewer@brand.example", pw)

	var index struct {
		Items []map[string]any `json:"items"`
	}
	get(t, c, srv, "/api/views", &index)
	if views := index.Items; len(views) != 1 || views[0]["name"] != "dice-bets" || views[0]["user_filter"] != "user_id" {
		t.Fatalf("views = %v", index)
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Next  *int64           `json:"next"`
	}
	get(t, c, srv, "/api/views/dice-bets?user_id="+strconv.FormatInt(alice, 10), &page)
	if len(page.Items) != 2 || page.Next != nil {
		t.Fatalf("alice's bets = %+v", page)
	}
	for _, it := range page.Items {
		if it["user_id"] != float64(alice) || it["amount"] != float64(100) || it["currency"] != "usd" || it["status"] != "settled" {
			t.Fatalf("bet = %v", it)
		}
		if _, ok := it["idempotency_key"]; ok {
			t.Fatalf("bet has the idempotency key: %v", it)
		}
	}
	get(t, c, srv, "/api/views/dice-bets?status=settled&limit=2", &page)
	if len(page.Items) != 2 || page.Next == nil {
		t.Fatalf("first page = %+v", page)
	}
	get(t, c, srv, "/api/views/dice-bets?status=settled&before="+strconv.FormatInt(*page.Next, 10), &page)
	if len(page.Items) != 1 || page.Items[0]["user_id"] != float64(alice) {
		t.Fatalf("second page = %+v", page)
	}
	get(t, c, srv, "/api/views/dice-bets?status=voided", &page)
	if len(page.Items) != 0 {
		t.Fatalf("voided = %+v", page)
	}
}

func get(t *testing.T, c *http.Client, srv *httptest.Server, path string, v any) {
	t.Helper()
	resp, err := c.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func post(t *testing.T, c *http.Client, srv *httptest.Server, path string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
	req.Header.Set("X-Admin", "1")
	req.Header.Set("Origin", origin)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s: %d %v", path, resp.StatusCode, out)
	}
	return out
}

// login logs a new console user in through the API, enrolling TOTP like an authenticator app.
func login(t *testing.T, srv *httptest.Server, email, pw string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := *srv.Client()
	c.Jar = jar
	out := post(t, &c, srv, "/api/login", map[string]string{"email": email, "password": pw})
	u, err := url.Parse(out["otpauth_uri"].(string))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(u.Query().Get("secret"))
	if err != nil {
		t.Fatal(err)
	}
	post(t, &c, srv, "/api/totp/enrol", map[string]string{"code": totp(secret, time.Now().Unix()/30)})
	return &c
}

// totp is RFC 6238 (SHA-1, 6 digits) for one time step.
func totp(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}
