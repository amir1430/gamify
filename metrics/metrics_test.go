package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecords(t *testing.T) {
	RPC(context.Background(), "arena.join", "1", 1101, time.Millisecond)
	if got := testutil.ToFloat64(rpcs.WithLabelValues("arena.join", "1101")); got != 1 {
		t.Fatalf("rpc count = %v", got)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /share/card/{file}", func(http.ResponseWriter, *http.Request) {})
	h := HTTP("app", mux)
	for _, path := range []string{"/share/card/abc.png", "/nope/xyz"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`gamify_http_request_duration_seconds_count{route="/share/card/{file}",server="app",status="200"} 1`,
		`gamify_http_request_duration_seconds_count{route="unmatched",server="app",status="404"} 1`,
		`gamify_rpc_total{code="1101",method="arena.join"} 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
}

// Two Apps in one process (tests) each export their own pool.
func TestPoolPerApp(t *testing.T) {
	for range 2 {
		pool, err := pgxpool.New(context.Background(), "postgres://localhost:1/none") // never connects
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		rec := httptest.NewRecorder()
		Handler(Pool(pool)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if !strings.Contains(rec.Body.String(), "gamify_db_pool_max_conns") {
			t.Fatal("/metrics lacks the pool")
		}
	}
}
