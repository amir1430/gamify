package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/auth/custom"
	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/ledgertest"
	"github.com/amir1430/gamify/ledger/postgres"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeTransport hands out "tok:<user>" so a test can read who logged in.
type fakeTransport struct{}

func (fakeTransport) Publish(context.Context, string, []byte) error { return nil }
func (fakeTransport) Disconnect(context.Context, int64) error       { return nil }
func (fakeTransport) Online(context.Context) (map[int64]int, error) { return nil, nil }
func (fakeTransport) Token(userID int64, _ string, _ time.Time) string {
	return "tok:" + strconv.FormatInt(userID, 10)
}
func (fakeTransport) Serve(context.Context, *realtime.Router) error { return nil }
func (fakeTransport) Ping(context.Context) error                    { return nil }

var secret = []byte("0123456789abcdef0123456789abcdef")

// jwt signs a custom-provider token for sub.
func jwt(sub string) json.RawMessage {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	in := enc(`{"alg":"HS256"}`) + "." + enc(fmt.Sprintf(`{"sub":%q,"aud":"game","exp":%d}`, sub, time.Now().Add(5*time.Minute).Unix()))
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(in))
	b, _ := json.Marshal(map[string]string{"token": in + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))})
	return b
}

// Runs only against a real Postgres (TEST_DATABASE_URL).
func testApp(t *testing.T, providers ...auth.Provider) *App {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, postgres.Migrations, "goose_ledger"); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, Config{Currencies: []ledger.CurrencyConfig{ledgertest.USD}},
		Drivers{Pool: pool, Ledger: postgres.New(pool), Realtime: fakeTransport{}, Auth: providers})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// login posts credential to /auth/{provider} and returns the status and the user it logged in.
func login(t *testing.T, a *App, provider string, credential []byte) (int, int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/"+provider, bytes.NewReader(credential)))
	if rec.Code != http.StatusOK {
		return rec.Code, 0
	}
	var body struct{ Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(body.Token, "tok:"), 10, 64)
	if err != nil {
		t.Fatalf("token %q", body.Token)
	}
	return rec.Code, id
}

func TestNewNeedsProviders(t *testing.T) {
	p, _ := custom.New(custom.Config{Secret: secret, Audience: "game"})
	for name, providers := range map[string][]auth.Provider{"none": nil, "duplicate": {p, p}} {
		_, err := New(context.Background(), Config{}, Drivers{Pool: &pgxpool.Pool{}, Ledger: postgres.New(nil),
			Realtime: fakeTransport{}, Auth: providers})
		if err == nil || !strings.Contains(err.Error(), "provider") {
			t.Errorf("%s: err = %v, want a provider error", name, err)
		}
	}
}

func TestLoginRoute(t *testing.T) {
	p, _ := custom.New(custom.Config{Secret: secret, Audience: "game"})
	a := testApp(t, p)
	sub := fmt.Sprint(rand.Int64())
	code, first := login(t, a, "custom", jwt(sub))
	if code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	if _, again := login(t, a, "custom", jwt(sub)); again != first {
		t.Fatalf("second login = user %d, want %d", again, first)
	}
	if code, _ := login(t, a, "telegram", []byte(`{"init_data":"x"}`)); code != http.StatusNotFound {
		t.Fatalf("unknown provider = %d, want 404", code)
	}
	if code, _ := login(t, a, "custom", []byte(`{"token":"a.b.c"}`)); code != http.StatusUnauthorized {
		t.Fatalf("bad credential = %d, want 401", code)
	}
	b, err := a.Ledger.Balances(context.Background(), first)
	if err != nil || b == nil {
		t.Fatalf("ledger accounts after login: %v %v", b, err)
	}
}

// liar claims to be telegram: its users must not land in Telegram players' accounts.
type liar struct{ sub string }

func (liar) Name() string { return "liar" }
func (l liar) Verify(context.Context, json.RawMessage, time.Time) (auth.Identity, error) {
	return auth.Identity{Provider: "telegram", Subject: l.sub}, nil
}

func TestProviderNameComesFromTheRoute(t *testing.T) {
	sub := fmt.Sprint(rand.Int64())
	a := testApp(t, liar{sub})
	_, id := login(t, a, "liar", []byte(`{}`))
	if s, err := a.Users.Subject(context.Background(), id, "liar"); err != nil || s != sub {
		t.Fatalf("liar identity = %q %v", s, err)
	}
	if _, err := a.Users.Subject(context.Background(), id, "telegram"); !errors.Is(err, auth.ErrNoIdentity) {
		t.Fatalf("stored as telegram: %v", err)
	}
}

// plain logs in whoever its credential names: a stand-in for a first login.
type plain struct{}

func (plain) Name() string { return "plain" }
func (plain) Verify(_ context.Context, c json.RawMessage, _ time.Time) (auth.Identity, error) {
	return auth.Identity{Subject: string(c)}, nil
}

func TestLinkRPC(t *testing.T) {
	p, _ := custom.New(custom.Config{Secret: secret, Audience: "game"})
	a, ctx := testApp(t, p, plain{}), context.Background()
	_, alice := login(t, a, "plain", []byte(fmt.Sprint(rand.Int64())))
	_, bob := login(t, a, "plain", []byte(fmt.Sprint(rand.Int64())))
	second := fmt.Sprint(rand.Int64())
	link := func(user int64, cred json.RawMessage) *realtime.ClientError {
		payload, _ := json.Marshal(map[string]any{"provider": "custom", "credential": cred})
		_, cerr := a.Router.Call(ctx, "auth.link", strconv.FormatInt(user, 10), payload)
		return cerr
	}
	if cerr := link(alice, jwt(second)); cerr != nil {
		t.Fatalf("link = %v", cerr)
	}
	if _, id := login(t, a, "custom", jwt(second)); id != alice {
		t.Fatalf("login with the linked identity = user %d, want alice %d", id, alice)
	}
	if cerr := link(bob, jwt(second)); cerr == nil || cerr.Code != ErrIdentityTaken.Code {
		t.Fatalf("bob linking alice's identity = %v, want %v", cerr, ErrIdentityTaken)
	}
	if cerr := link(alice, jwt(fmt.Sprint(rand.Int64()))); cerr == nil || cerr.Code != ErrProviderLinked.Code {
		t.Fatalf("alice's second custom identity = %v, want %v", cerr, ErrProviderLinked)
	}
	if cerr := link(bob, json.RawMessage(`{"token":"a.b.c"}`)); cerr == nil || cerr.Code != ErrInvalidCredential.Code {
		t.Fatalf("bad credential = %v, want %v", cerr, ErrInvalidCredential)
	}
	payload := []byte(`{"provider":"nope","credential":{}}`)
	if _, cerr := a.Router.Call(ctx, "auth.link", strconv.FormatInt(bob, 10), payload); cerr == nil || cerr.Code != realtime.CodeBadPayload {
		t.Fatalf("unknown provider = %v", cerr)
	}
}

// A login check refuses the login with 403 and its message, and gives no token.
func TestLoginCheck(t *testing.T) {
	p, _ := custom.New(custom.Config{Secret: secret, Audience: "game"})
	a := testApp(t, p)
	sub := fmt.Sprint(rand.Int64())
	_, blocked := login(t, a, "custom", jwt(sub))
	a.LoginCheck(func(_ context.Context, userID int64) error {
		if userID == blocked {
			return &realtime.ClientError{Code: 1108, Message: "this account is suspended"}
		}
		return nil
	})
	if code, other := login(t, a, "custom", jwt(fmt.Sprint(rand.Int64()))); code != http.StatusOK || other == 0 {
		t.Fatalf("other user = %d", code)
	}
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/custom", bytes.NewReader(jwt(sub))))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "this account is suspended") || strings.Contains(rec.Body.String(), "tok:") {
		t.Fatalf("blocked login = %d %s", rec.Code, rec.Body)
	}
	a.LoginCheck(func(context.Context, int64) error { return errors.New("db down") })
	if code, _ := login(t, a, "custom", jwt(fmt.Sprint(rand.Int64()))); code != http.StatusForbidden {
		t.Fatalf("failing check = %d, want 403", code)
	}
}

// OnLogin sees every successful login with whether the user is new and the verified identity; its
// error doesn't stop the login, and a refused login never reaches it.
func TestOnLogin(t *testing.T) {
	p, _ := custom.New(custom.Config{Secret: secret, Audience: "game"})
	a := testApp(t, p)
	type seen struct {
		user    int64
		created bool
		subject string
	}
	var got []seen
	a.OnLogin(func(_ context.Context, l auth.Login, id auth.Identity) error {
		got = append(got, seen{l.ID, l.Created, id.Subject})
		return errors.New("hook failed")
	})
	sub := fmt.Sprint(rand.Int64())
	_, first := login(t, a, "custom", jwt(sub))
	_, again := login(t, a, "custom", jwt(sub))
	if first == 0 || again != first {
		t.Fatalf("logins %d %d", first, again)
	}
	if len(got) != 2 || got[0] != (seen{first, true, sub}) || got[1] != (seen{first, false, sub}) {
		t.Fatalf("hook saw %+v", got)
	}
	a.LoginCheck(func(context.Context, int64) error { return errors.New("refused") })
	login(t, a, "custom", jwt(sub))
	if len(got) != 2 {
		t.Fatal("hook ran for a refused login")
	}
}
