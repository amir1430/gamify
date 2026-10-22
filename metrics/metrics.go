// Package metrics holds the Prometheus metrics and the request logs. Served by Handler on its own
// port (METRICS_ADDR), never on the public Mini App port.
package metrics

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// SlowRPC: an RPC slower than this is logged as a warning (Centrifugo's proxy timeout is above it).
const SlowRPC = time.Second

var (
	// rpcs is labelled by method and result code ("0" = ok); method is only ever a registered one,
	// so a client can't grow the label set.
	rpcs = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gamify_rpc_total", Help: "RPC calls by method and result code (0 = ok).",
	}, []string{"method", "code"})
	rpcSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gamify_rpc_duration_seconds", Help: "RPC handling time by method.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method"})
	httpReqs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gamify_http_request_duration_seconds", Help: "HTTP handling time by server, route and status.",
		Buckets: prometheus.DefBuckets,
	}, []string{"server", "route", "status"})

	mmTickets = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gamify_matchmaking_tickets", Help: "Tickets waiting in a matchmaking queue.",
	}, []string{"queue"})
	mmWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gamify_matchmaking_wait_seconds", Help: "Time a ticket waited before its match formed.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 300},
	}, []string{"queue"})
	mmMatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gamify_matchmaking_matches_total", Help: "Matches by queue and outcome (formed, settled, cancelled).",
	}, []string{"queue", "outcome"})
)

// MatchmakingTickets sets the number of tickets waiting in queue.
func MatchmakingTickets(queue string, n int) { mmTickets.WithLabelValues(queue).Set(float64(n)) }

// MatchmakingWait records how long a ticket waited before its match formed.
func MatchmakingWait(queue string, d time.Duration) {
	mmWait.WithLabelValues(queue).Observe(d.Seconds())
}

// MatchmakingMatch counts a match transition: outcome is "formed", "settled" or "cancelled".
func MatchmakingMatch(queue, outcome string) { mmMatches.WithLabelValues(queue, outcome).Inc() }

// RPC records one finished RPC and logs it: debug always, warn when slow, error on code 1000.
func RPC(ctx context.Context, method, user string, code uint32, took time.Duration) {
	rpcs.WithLabelValues(method, strconv.FormatUint(uint64(code), 10)).Inc()
	rpcSeconds.WithLabelValues(method).Observe(took.Seconds())
	level := slog.LevelDebug
	if took >= SlowRPC {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "rpc", "method", method, "user", user, "code", code, "ms", took.Milliseconds())
}

// HTTP times and logs every request of a server (health checks: metrics only, no log line). The
// route label is the ServeMux pattern that matched, so wrap the mux itself.
func HTTP(server string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		took := time.Since(start)
		route := route(r.Pattern)
		httpReqs.WithLabelValues(server, route, strconv.Itoa(sw.status)).Observe(took.Seconds())
		if route == "/healthz" || route == "/readyz" {
			return
		}
		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelError
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		slog.Log(r.Context(), level, "http", "server", server, "method", r.Method, "route", route,
			"status", sw.status, "ms", took.Milliseconds(), "ip", ip)
	})
}

// route is a ServeMux pattern without its method ("GET /healthz" -> "/healthz").
func route(pattern string) string {
	if pattern == "" {
		return "unmatched" // raw paths would let anyone grow the label set
	}
	if method, path, ok := strings.Cut(pattern, " "); ok && !strings.Contains(method, "/") {
		return path
	}
	return pattern
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer (Flush, deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Pool exports the Postgres pool's state: acquired near max or a growing wait = pool starvation.
// Each pool gets its own registry (serve it with Handler), so two Apps in one process don't clash.
func Pool(pool *pgxpool.Pool) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	gauge := func(name, help string, f func(*pgxpool.Stat) float64) {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 { return f(pool.Stat()) }))
	}
	counter := func(name, help string, f func(*pgxpool.Stat) float64) {
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, func() float64 { return f(pool.Stat()) }))
	}
	gauge("gamify_db_pool_max_conns", "Pool size.", func(s *pgxpool.Stat) float64 { return float64(s.MaxConns()) })
	gauge("gamify_db_pool_acquired_conns", "Connections in use.", func(s *pgxpool.Stat) float64 { return float64(s.AcquiredConns()) })
	gauge("gamify_db_pool_idle_conns", "Idle connections.", func(s *pgxpool.Stat) float64 { return float64(s.IdleConns()) })
	counter("gamify_db_pool_empty_acquire_total", "Acquires that had to wait for a connection.",
		func(s *pgxpool.Stat) float64 { return float64(s.EmptyAcquireCount()) })
	counter("gamify_db_pool_acquire_wait_seconds_total", "Time spent waiting for a connection.",
		func(s *pgxpool.Stat) float64 { return s.EmptyAcquireWaitTime().Seconds() })
	return reg
}

// Handler serves /metrics: the process-wide metrics (Go runtime and process included) plus extra
// registries, such as an App's pool.
func Handler(extra ...prometheus.Gatherer) http.Handler {
	gatherers := append(prometheus.Gatherers{prometheus.DefaultGatherer}, extra...)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(prometheus.DefaultRegisterer, promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{})))
	return mux
}
