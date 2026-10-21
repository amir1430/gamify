package admin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/app"
)

// Admin is the console: its API and UI on Config.Addr.
type Admin struct {
	a      *app.App
	cfg    Config
	q      *store.Queries
	totp   totp
	mux    *http.ServeMux
	routes map[string]route // pattern → what it needs
	h      http.Handler
	logins chan struct{} // login attempts in flight, at most maxLogins
	views  views         // brand views (View)
}

// Register migrates the module's tables and adds the console's server to a.Run. Call it after the
// games are registered.
func Register(ctx context.Context, a *app.App, cfg Config) (*Admin, error) {
	cfg, err := cfg.validate()
	if err != nil {
		return nil, err
	}
	if err := a.Migrate(ctx, Migrations, gooseTable); err != nil {
		return nil, err
	}
	adm := &Admin{a: a, cfg: cfg, q: store.New(a.Pool), totp: totp{key: cfg.TOTPKey},
		mux: http.NewServeMux(), routes: map[string]route{}, logins: make(chan struct{}, maxLogins)}
	adm.h = adm.guard(adm.mux)
	adm.register()
	a.Reconcile(adm.reconcile)
	a.Serve("admin", &http.Server{Addr: cfg.Addr, Handler: adm.Handler(), ReadHeaderTimeout: 5 * time.Second})
	return adm, nil
}

// Handler is the console's HTTP handler (what Register serves on Addr).
func (adm *Admin) Handler() http.Handler { return adm.h }

// validate returns cfg with its slices copied and Origin without a trailing slash.
func (cfg Config) validate() (Config, error) {
	if cfg.Addr == "" {
		return cfg, errors.New("admin: Config.Addr is required")
	}
	origin := strings.TrimSuffix(cfg.Origin, "/")
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return cfg, fmt.Errorf("admin: Config.Origin %q is not an http(s) origin like https://admin.brand.example", cfg.Origin)
	}
	cfg.Origin = origin
	if len(cfg.TOTPKey) != 32 {
		return cfg, fmt.Errorf("admin: Config.TOTPKey is %d bytes, want 32 (AES-256)", len(cfg.TOTPKey))
	}
	cfg.TOTPKey = append([]byte(nil), cfg.TOTPKey...)
	for _, p := range cfg.AllowIPs {
		if !p.IsValid() {
			return cfg, fmt.Errorf("admin: Config.AllowIPs has an invalid prefix %q", p)
		}
	}
	cfg.AllowIPs = append([]netip.Prefix(nil), cfg.AllowIPs...)
	for c, n := range cfg.ApproveAbove {
		if n < 0 {
			return cfg, fmt.Errorf("admin: Config.ApproveAbove[%s] is %d, want >= 0", c, n)
		}
	}
	cfg.ApproveAbove = maps.Clone(cfg.ApproveAbove)
	perms := make(map[Perm][]Role, len(cfg.Perms))
	for p, roles := range cfg.Perms {
		if p == "" || builtin(p) {
			return cfg, fmt.Errorf("admin: Config.Perms key %q is empty or a built-in permission", p)
		}
		for _, r := range roles {
			if !r.Valid() {
				return cfg, fmt.Errorf("admin: Config.Perms[%s] has an unknown role %q", p, r)
			}
		}
		perms[p] = slices.Clone(roles)
	}
	cfg.Perms = perms
	return cfg, nil
}

// register adds every route.
func (adm *Admin) register() {
	adm.public("POST /api/login", adm.limited(adm.login))
	if adm.cfg.TelegramBotToken != "" {
		adm.public("POST /api/login/telegram", adm.limited(adm.loginTelegram))
	}
	adm.public("POST /api/logout", adm.logout)
	adm.public("GET /api/brand/logo", adm.brandLogo)
	adm.enrolment("POST /api/totp/enrol", adm.enrol)
	adm.handle("GET /api/me", "", adm.me)
	adm.registerReads()
	adm.registerWrites()
	adm.registerAdmins()
	adm.registerAdjustments()
	adm.registerViews()
	adm.mux.HandleFunc("/", adm.serveUI) // the UI, and JSON 404s for unknown /api paths
}
