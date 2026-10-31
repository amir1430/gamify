// Package app wires the platform: Postgres, the ledger and realtime drivers it is given, users,
// game limits, login through the brand's auth providers, health checks and the core RPCs. Optional modules (fairness, games)
// register themselves on it. A brand's main builds the
// drivers, calls New, registers its games and calls Run.
package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/metrics"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReconcileEvery is how often unfinished operations are settled again (after a crash or a driver outage).
const ReconcileEvery = 15 * time.Second

type Config struct {
	Currencies     []ledger.CurrencyConfig // the first is the default
	HTTPAddr       string                  // login, health (default :8080)
	MetricsAddr    string                  // Prometheus /metrics, keep it private (default :9090, "off")
	AllowedOrigins []string                // the Mini App's origins (CORS)
	DevFaucet      bool                    // wallet.faucet: free test money. Never with real money.
}

// Drivers are what the brand chose to run on.
type Drivers struct {
	Pool     *pgxpool.Pool
	Ledger   ledger.Driver
	Realtime realtime.Transport
	Auth     []auth.Provider // at least one; each logs in at POST /auth/{name}
}

// Errors of the auth.link RPC (platform codes stay below the games' 1100).
var (
	ErrInvalidCredential = &realtime.ClientError{Code: 1010, Message: "invalid credential"}
	ErrIdentityTaken     = &realtime.ClientError{Code: 1011, Message: "this login belongs to another account"}
	ErrProviderLinked    = &realtime.ClientError{Code: 1012, Message: "this account already has a login of this kind"}
)

type namedServer struct {
	name string
	*http.Server
}

type App struct {
	Pool     *pgxpool.Pool
	Ledger   *ledger.Ledger
	Games    *gameconfig.Service
	Users    *auth.Users
	Realtime realtime.Transport
	Router   *realtime.Router
	HTTP     *http.ServeMux // add routes before Run

	cfg       Config
	providers map[string]auth.Provider
	reconcile []func(context.Context)
	logins    []func(ctx context.Context, userID int64) error
	onLogin   []func(ctx context.Context, l auth.Login, id auth.Identity) error
	loops     []loop
	extra     []namedServer
	historyMu sync.Mutex
	history   map[string]HistorySource
}

// New migrates the platform's tables, readies the ledger and loads the game limits.
func New(ctx context.Context, cfg Config, d Drivers) (*App, error) {
	providers := map[string]auth.Provider{}
	for _, p := range d.Auth {
		name := p.Name()
		if name == "" || strings.ContainsAny(name, "/ ") || providers[name] != nil {
			return nil, fmt.Errorf("app: auth provider %q: names must be unique, non-empty, without / or space", name)
		}
		providers[name] = p
	}
	if len(providers) == 0 {
		return nil, errors.New("app: at least one auth provider is required")
	}
	if d.Pool == nil || d.Ledger == nil || d.Realtime == nil {
		return nil, errors.New("app: pool, ledger and realtime are required")
	}
	if err := db.Migrate(ctx, d.Pool, db.Migrations, "goose_platform"); err != nil {
		return nil, err
	}
	l, err := ledger.New(d.Ledger, cfg.Currencies...)
	if err != nil {
		return nil, err
	}
	if err := l.Init(ctx); err != nil {
		return nil, err
	}
	games := gameconfig.NewService(d.Pool, l)
	if err := games.Load(ctx); err != nil {
		return nil, err
	}
	a := &App{Pool: d.Pool, Ledger: l, Games: games,
		Users: auth.NewUsers(d.Pool), Realtime: d.Realtime, Router: realtime.NewRouter(), HTTP: http.NewServeMux(), cfg: cfg, providers: providers}
	a.routes()
	a.methods()
	return a, nil
}

// Migrate runs a module's own migrations ("migrations/*.sql" in fsys), versioned in table.
func (a *App) Migrate(ctx context.Context, fsys fs.FS, table string) error {
	return db.Migrate(ctx, a.Pool, fsys, table)
}

// Serve adds a server that Run starts and shuts down with the others (the admin's own listener).
// Call it before Run: Run reads the list without a lock.
func (a *App) Serve(name string, srv *http.Server) {
	a.extra = append(a.extra, namedServer{name, srv})
}

// Reconcile registers a job run every ReconcileEvery (a game finishing its pending operations).
func (a *App) Reconcile(f func(context.Context)) { a.reconcile = append(a.reconcile, f) }

// OnLogin adds a hook every successful login runs, after the checks and before the token is sent:
// l says whether the user was just created, id is the verified identity (with the provider's Meta,
// such as Telegram's start_param), for referral attribution, welcome flows, analytics. An error is
// logged and the login goes on (refusing a login is LoginCheck's job). Add hooks before Run.
func (a *App) OnLogin(f func(ctx context.Context, l auth.Login, id auth.Identity) error) {
	a.onLogin = append(a.onLogin, f)
}

// LoginCheck adds a check every login runs once the user is known (a suspended player): an error
// refuses the login with 403 and no token. A *realtime.ClientError's message is sent as
// {"error": message}; any other error is logged and the body stays empty. Add checks before Run.
func (a *App) LoginCheck(f func(ctx context.Context, userID int64) error) {
	a.logins = append(a.logins, f)
}

// Loop registers f to run every `every` while Run is going (a module's own ticker, like a matcher).
// Call it before Run: Run reads the list without a lock.
func (a *App) Loop(every time.Duration, f func(context.Context)) {
	a.loops = append(a.loops, loop{every, f})
}

type loop struct {
	every time.Duration
	f     func(context.Context)
}

// RunReconcile runs every registered reconcile job once (Run does it every ReconcileEvery).
func (a *App) RunReconcile(ctx context.Context) {
	for _, f := range a.reconcile {
		f(ctx)
	}
}

// Balances reads the user's balances and pushes them to their other tabs.
func (a *App) Balances(ctx context.Context, userID int64) (ledger.Balances, error) {
	b, err := a.Ledger.Balances(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := realtime.PublishJSON(ctx, a.Realtime, realtime.UserChannel(userID), map[string]any{"type": "balances", "balances": b}); err != nil {
		slog.WarnContext(ctx, "push balances", "user", userID, "err", err) // the caller still gets them
	}
	return b, nil
}

func (a *App) routes() {
	r := a.HTTP
	r.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		checks := map[string]error{"postgres": a.Pool.Ping(ctx), "realtime": a.Realtime.Ping(ctx)}
		if p, ok := a.Ledger.Driver().(interface{ Ping(context.Context) error }); ok {
			checks["ledger"] = p.Ping(ctx)
		}
		status, body := http.StatusOK, map[string]string{}
		for name, err := range checks {
			body[name] = "ok"
			if err != nil {
				status, body[name] = http.StatusServiceUnavailable, err.Error()
			}
		}
		writeJSON(w, status, body)
	})
	// Login: a provider's credential in, a realtime connection token out.
	r.HandleFunc("POST /auth/{provider}", func(w http.ResponseWriter, req *http.Request) {
		p := a.providers[req.PathValue("provider")]
		if p == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		credential, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 64<<10))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, now := req.Context(), time.Now()
		id, err := p.Verify(ctx, credential, now)
		id.Provider = p.Name() // the route's provider, whatever a brand's driver put there
		if err != nil {
			if !errors.Is(err, auth.ErrInvalidCredential) {
				slog.ErrorContext(ctx, "verify", "provider", p.Name(), "err", err)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		u, err := a.Users.Upsert(ctx, id)
		if err == nil {
			err = a.Ledger.EnsureUser(ctx, u.ID)
		}
		if err != nil {
			slog.ErrorContext(ctx, "login", "provider", id.Provider, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, check := range a.logins {
			if err := check(ctx, u.ID); err != nil {
				var ce *realtime.ClientError
				if !errors.As(err, &ce) {
					slog.ErrorContext(ctx, "login check", "user", u.ID, "err", err)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				writeJSON(w, http.StatusForbidden, map[string]string{"error": ce.Message})
				return
			}
		}
		for _, hook := range a.onLogin {
			if err := hook(ctx, u, id); err != nil {
				slog.ErrorContext(ctx, "login hook", "user", u.ID, "err", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": a.Realtime.Token(u.ID, cmp.Or(id.DisplayName, id.Username), now)})
	})
}

// handler is the public HTTP server: every route of a.HTTP, timed, recovered and CORS-enabled.
func (a *App) handler() http.Handler {
	return metrics.HTTP("app", recoverer(cors(a.cfg.AllowedOrigins, a.HTTP)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// methods registers the platform's RPCs: auth, wallet, game limits.
func (a *App) methods() {
	r := a.Router
	// auth.link attaches another login to the connection's user, so either one logs into the account.
	r.Handle("auth.link", realtime.JSON(func(ctx context.Context, userID int64, req struct {
		Provider   string          `json:"provider"`
		Credential json.RawMessage `json:"credential"`
	}) (struct{}, error) {
		p := a.providers[req.Provider]
		if p == nil {
			return struct{}{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "unknown provider"}
		}
		id, err := p.Verify(ctx, req.Credential, time.Now())
		id.Provider = p.Name()
		if errors.Is(err, auth.ErrInvalidCredential) {
			return struct{}{}, ErrInvalidCredential
		}
		if err != nil {
			return struct{}{}, err
		}
		switch err := a.Users.Link(ctx, userID, id); {
		case errors.Is(err, auth.ErrIdentityTaken):
			return struct{}{}, ErrIdentityTaken
		case errors.Is(err, auth.ErrProviderLinked):
			return struct{}{}, ErrProviderLinked
		case err != nil:
			return struct{}{}, err
		}
		return struct{}{}, nil
	}))
	r.Handle("wallet.balances", realtime.JSON(func(ctx context.Context, userID int64, _ struct{}) (ledger.Balances, error) {
		return a.Ledger.Balances(ctx, userID)
	}))
	if a.cfg.DevFaucet {
		r.Handle("wallet.faucet", realtime.JSON(func(ctx context.Context, userID int64, _ struct{}) (ledger.Balances, error) {
			if err := a.Ledger.TopUp(ctx, a.Ledger.Default(), userID, 10_000); err != nil {
				return nil, err
			}
			return a.Balances(ctx, userID)
		}))
	}
	r.Handle("games.config", realtime.JSON(func(context.Context, int64, struct{}) (any, error) {
		return map[string]any{"games": a.Games.Public(), "off": a.Games.OffCurrencies(), "currencies": a.Ledger.Currencies()}, nil
	}))
}

// Run serves until ctx ends: realtime RPCs, HTTP, metrics, the reconcile and config reload loops.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	servers := []*http.Server{{Addr: cmp.Or(a.cfg.HTTPAddr, ":8080"), Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second}}
	if addr := cmp.Or(a.cfg.MetricsAddr, ":9090"); addr != "off" {
		servers = append(servers, &http.Server{Addr: addr, Handler: metrics.Handler(metrics.Pool(a.Pool)), ReadHeaderTimeout: 5 * time.Second})
	}
	for _, e := range a.extra {
		servers = append(servers, e.Server)
	}
	errc := make(chan error, 1+len(servers))
	go func() { errc <- a.Realtime.Serve(ctx, a.Router) }()
	for _, srv := range servers {
		go func() { errc <- srv.ListenAndServe() }()
	}
	go a.Games.Run(ctx)
	go func() {
		t := time.NewTicker(ReconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.RunReconcile(ctx)
			}
		}
	}()
	for _, l := range a.loops {
		go func() {
			t := time.NewTicker(l.every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					l.f(ctx)
				}
			}
		}()
	}
	slog.Info("started", "http", servers[0].Addr, "methods", len(a.Router.Methods()))
	for _, e := range a.extra {
		slog.Info("listening", "server", e.name, "addr", e.Addr)
	}

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancel()
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	errs := []error{err}
	for _, srv := range servers {
		errs = append(errs, srv.Shutdown(shutdown))
	}
	return errors.Join(errs...)
}

// cors lets the Mini App, served from its own domain, call the API from the browser.
func cors(origins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && slices.Contains(origins, o) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a handler's panic into a logged 500 instead of a dropped connection.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				slog.ErrorContext(r.Context(), "http panic", "path", r.URL.Path, "panic", p)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
