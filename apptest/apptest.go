// Package apptest runs a real gamify App in a test: Postgres (a fresh database per App, dropped at
// the end), the Postgres ledger driver and an in-memory realtime transport, so a brand tests its
// games' RPCs without Centrifugo. Needs TEST_DATABASE_URL (any database on the server; the test
// creates its own next to it) and skips without it.
//
//	a := apptest.New(t)
//	fair, _ := fairness.Register(ctx, a.App, apptest.Sealer(t))
//	arena.Register(ctx, a.App, fair)
//	alice := a.User("alice")
//	a.Fund(alice, "usd", 10_000)
//	res, cerr := apptest.Call[arena.JoinResult](a, alice, "arena.join", arena.JoinRequest{…})
package apptest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/postgres"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Currencies are the default ones: usd (minor units, no welcome grant) and tickets (1000 granted
// on the first login, play-only).
var Currencies = []ledger.CurrencyConfig{
	{Code: "usd", Ledger: 1, MinAmount: 1},
	{Code: "tickets", Ledger: 2, MinAmount: 10, Welcome: 1000},
}

// App is a running platform for one test. Its embedded *app.App is what games' Register take.
type App struct {
	*app.App
	t         testing.TB
	Transport *Transport
}

type options struct{ currencies []ledger.CurrencyConfig }

type Option func(*options)

// WithCurrencies replaces the default currencies.
func WithCurrencies(c ...ledger.CurrencyConfig) Option {
	return func(o *options) { o.currencies = c }
}

// New starts an App on a fresh database. Everything is released when the test ends.
func New(t testing.TB, opts ...Option) *App {
	t.Helper()
	o := options{currencies: Currencies}
	for _, f := range opts {
		f(&o)
	}
	ctx := context.Background()
	pool := freshDatabase(t)
	if err := db.Migrate(ctx, pool, postgres.Migrations, "goose_ledger"); err != nil {
		t.Fatal(err)
	}
	tr := &Transport{published: map[string][][]byte{}}
	a, err := app.New(ctx, app.Config{Currencies: o.currencies},
		app.Drivers{Pool: pool, Ledger: postgres.New(pool), Realtime: tr, Auth: []auth.Provider{provider{}}})
	if err != nil {
		t.Fatal(err)
	}
	return &App{App: a, t: t, Transport: tr}
}

// User logs in the player called name (the same name is the same user) and returns their ID. Their
// ledger accounts exist and the welcome grants are paid, as after a real login.
func (a *App) User(name string) int64 {
	a.t.Helper()
	ctx := context.Background()
	l, err := a.Users.Upsert(ctx, auth.Identity{Provider: provider{}.Name(), Subject: name, DisplayName: name})
	if err == nil {
		err = a.Ledger.EnsureUser(ctx, l.ID)
	}
	if err != nil {
		a.t.Fatal(err)
	}
	return l.ID
}

// Fund gives the user amount (minor units) of currency c.
func (a *App) Fund(userID int64, c ledger.Currency, amount int64) {
	a.t.Helper()
	if err := a.Ledger.TopUp(context.Background(), c, userID, amount); err != nil {
		a.t.Fatal(err)
	}
}

// Balance is the user's balance of c, read from the ledger.
func (a *App) Balance(userID int64, c ledger.Currency) int64 {
	a.t.Helper()
	b, err := a.Ledger.Balances(context.Background(), userID)
	if err != nil {
		a.t.Fatal(err)
	}
	return b[c]
}

// Call makes an RPC as the user, exactly as the transport would: req is marshalled to JSON (nil
// sends {}), the result is the handler's raw JSON or the client error it returned.
func (a *App) Call(userID int64, method string, req any) (json.RawMessage, *realtime.ClientError) {
	a.t.Helper()
	data := []byte("{}")
	if req != nil {
		var err error
		if data, err = json.Marshal(req); err != nil {
			a.t.Fatal(err)
		}
	}
	return a.Router.Call(context.Background(), method, strconv.FormatInt(userID, 10), data)
}

// Call is App.Call with the result decoded into Resp.
func Call[Resp any](a *App, userID int64, method string, req any) (Resp, *realtime.ClientError) {
	a.t.Helper()
	var out Resp
	raw, cerr := a.Call(userID, method, req)
	if cerr != nil {
		return out, cerr
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		a.t.Fatalf("%s: decoding %s: %v", method, raw, err)
	}
	return out, nil
}

// Pushes are the messages published to channel so far, oldest first.
func (a *App) Pushes(channel string) [][]byte { return a.Transport.Published(channel) }

// ReconcileNow runs the games' reconcile jobs once, as Run does every app.ReconcileEvery.
func (a *App) ReconcileNow() { a.RunReconcile(context.Background()) }

// Sealer is a fixed-key seed sealer for fairness.Register in tests. Never use it outside tests.
func Sealer(t testing.TB) *fairness.Sealer {
	s, err := fairness.NewSealer("apptest", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Transport is the in-memory realtime.Transport: it records what's published and disconnected.
type Transport struct {
	mu           sync.Mutex
	published    map[string][][]byte
	disconnected []int64
	online       map[int64]int
}

var _ realtime.Transport = (*Transport)(nil)

func (tr *Transport) Publish(_ context.Context, channel string, data []byte) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.published[channel] = append(tr.published[channel], bytes.Clone(data))
	return nil
}

func (tr *Transport) Disconnect(_ context.Context, userID int64) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.disconnected = append(tr.disconnected, userID)
	return nil
}

// SetOnline makes Online report exactly these users (one connection each); the default is nobody.
func (tr *Transport) SetOnline(userIDs ...int64) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.online = map[int64]int{}
	for _, id := range userIDs {
		tr.online[id] = 1
	}
}

func (tr *Transport) Online(context.Context) (map[int64]int, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return maps.Clone(tr.online), nil
}

func (tr *Transport) Token(userID int64, _ string, _ time.Time) string {
	return "apptest:" + strconv.FormatInt(userID, 10)
}

func (tr *Transport) Serve(ctx context.Context, _ *realtime.Router) error {
	<-ctx.Done()
	return nil
}

func (tr *Transport) Ping(context.Context) error { return nil }

// Published is a copy of what was published to channel.
func (tr *Transport) Published(channel string) [][]byte {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.published[channel])
}

// Disconnected lists the users disconnected so far.
func (tr *Transport) Disconnected() []int64 {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return slices.Clone(tr.disconnected)
}

// provider logs in whoever its credential names; User uses it. Only an apptest App has it.
type provider struct{}

func (provider) Name() string { return "apptest" }

func (provider) Verify(_ context.Context, c json.RawMessage, _ time.Time) (auth.Identity, error) {
	var name string
	if err := json.Unmarshal(c, &name); err != nil || name == "" {
		return auth.Identity{}, auth.ErrInvalidCredential
	}
	return auth.Identity{Provider: "apptest", Subject: name, DisplayName: name}, nil
}

// freshDatabase creates an empty database next to TEST_DATABASE_URL's and drops it at the end.
func freshDatabase(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn) // takes the pool_* settings (pool_max_conns) out of the DSN
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg.ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	var b [6]byte
	rand.Read(b[:])
	name := "apptest_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
		admin.Close(ctx)
	})
	return pool
}
