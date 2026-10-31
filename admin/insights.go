package admin

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"time"

	"github.com/amir1430/gamify/auth"
)

const (
	defaultSignupDays = 30
	defaultOnline     = 100
	maxOnline         = 500
	pingTimeout       = 2 * time.Second
	modulePath        = "github.com/amir1430/gamify"
)

func (adm *Admin) registerInsights() {
	adm.handle("GET /api/online", PermView, adm.online)
	adm.handle("GET /api/system", PermView, adm.system)
}

// connections is every connected user's open connections; nil when the transport can't say (a
// player list still loads while Centrifugo is down).
func (adm *Admin) connections(ctx context.Context) map[int64]int {
	on, err := adm.a.Realtime.Online(ctx)
	if err != nil {
		return nil
	}
	return on
}

// playerRow is a player with their open connections (0: offline; null: unknown).
type playerRow struct {
	auth.User
	Connections *int `json:"connections"`
}

func withConnections(us []auth.User, on map[int64]int) []playerRow {
	out := make([]playerRow, len(us))
	for i, u := range us {
		out[i].User = u
		if on != nil {
			n := on[u.ID]
			out[i].Connections = &n
		}
	}
	return out
}

// online lists the connected players, newest account first (?limit=, default 100, at most 500),
// with how many are connected in all (total) and their connections.
func (adm *Admin) online(w http.ResponseWriter, r *http.Request, _ *session) error {
	limit := defaultOnline
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return badRequest("limit must be a positive integer")
		}
		limit = min(n, maxOnline)
	}
	on, err := adm.a.Realtime.Online(r.Context())
	if err != nil {
		return err
	}
	conns := 0
	for _, n := range on {
		conns += n
	}
	// Every connected ID, so one without a user (not ours) never shortens the page.
	us, err := adm.a.Users.Get(r.Context(), slices.Collect(maps.Keys(on))...) // newest first
	if err != nil {
		return err
	}
	us = us[:min(len(us), limit)]
	writeJSON(w, http.StatusOK, map[string]any{"total": len(on), "connections": conns, "items": withConnections(us, on)})
	return nil
}

type check struct {
	OK    bool    `json:"ok"`
	MS    float64 `json:"ms"`
	Error *string `json:"error"`
}

func ping(ctx context.Context, f func(context.Context) error) check {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	start := time.Now()
	err := f(ctx)
	c := check{OK: err == nil, MS: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		s := err.Error()
		c.Error = &s
	}
	return c
}

// version is gamify's module version in this binary ("(devel)" when built from its own checkout).
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if bi.Main.Path == modulePath {
		return cmp.Or(bi.Main.Version, "(devel)")
	}
	for _, d := range bi.Deps {
		if d.Path == modulePath {
			if d.Replace != nil {
				return d.Version + " => " + d.Replace.Path
			}
			return d.Version
		}
	}
	return "unknown"
}

// system is the console's status page (Nakama's Status and Configuration): build, process,
// connection pool, the dependencies' health, the currencies, the RPCs and activity sources the
// brand registered, and what each role may do. Nothing secret: no keys, tokens or addresses.
func (adm *Admin) system(w http.ResponseWriter, r *http.Request, _ *session) error {
	ctx := r.Context()
	health := map[string]check{"postgres": ping(ctx, adm.a.Pool.Ping), "realtime": ping(ctx, adm.a.Realtime.Ping)}
	if p, ok := adm.a.Ledger.Driver().(interface{ Ping(context.Context) error }); ok {
		health["ledger"] = ping(ctx, p.Ping)
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	ps := adm.a.Pool.Stat()

	cur := []map[string]any{}
	for _, c := range adm.a.Ledger.Currencies() {
		var above *int64
		if n, ok := adm.cfg.ApproveAbove[c]; ok {
			above = &n
		}
		cur = append(cur, map[string]any{"code": c, "decimals": adm.a.Ledger.Decimals(c),
			"min_amount": adm.a.Ledger.MinAmount(c), "approve_above": above, "default": c == adm.a.Ledger.Default()})
	}

	history := slices.Sorted(maps.Keys(adm.a.HistorySources()))
	roles := map[Role][]string{}
	for _, role := range []Role{RoleOwner, RoleOperator, RoleSupport, RoleViewer} {
		roles[role] = adm.perms(role)
	}
	rpcs := adm.a.Router.Methods() // sorted
	adm.views.mu.RLock()
	nviews := len(adm.views.order)
	adm.views.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"version":    version(),
		"go":         runtime.Version(),
		"started_at": adm.started,
		"runtime": map[string]any{
			"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0),
			"goroutines": runtime.NumGoroutine(), "heap_bytes": mem.HeapAlloc, "sys_bytes": mem.Sys, "gc_runs": mem.NumGC,
		},
		"pool": map[string]any{
			"max": ps.MaxConns(), "total": ps.TotalConns(), "idle": ps.IdleConns(), "in_use": ps.AcquiredConns(),
			"acquires": ps.AcquireCount(), "waited": ps.EmptyAcquireCount(),
		},
		"health":     health,
		"currencies": cur,
		"rpcs":       rpcs,
		"history":    history,
		"roles":      roles,
		"console": map[string]any{
			"telegram_login": adm.cfg.TelegramBotToken != "",
			"fairness":       adm.cfg.Fairness != nil,
			"ip_allowlist":   len(adm.cfg.AllowIPs),
			"views":          nviews,
			"brand_perms":    len(adm.cfg.Perms),
		},
	})
	return nil
}
