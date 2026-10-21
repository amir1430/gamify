package drivers

import (
	"context"
	"os"
	"testing"

	"github.com/amir1430/gamify/config"
)

// Runs only against a real Postgres (TEST_DATABASE_URL). Centrifugo isn't needed: the transport
// doesn't dial until its first call.
func TestBuildIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	c, err := config.Parse([]byte(`
postgres: {dsn: "${DB}"}
centrifugo: {api_addr: "127.0.0.1:1", api_key: k, token_secret: s, proxy_endpoint: "grpc://x:1"}
http: {allowed_origins: ["http://localhost"]}
`), func(k string) (string, bool) { return url, k == "DB" })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, closeAll, err := Build(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Ledger == nil || d.Realtime == nil {
		t.Fatalf("drivers: %+v", d)
	}
	if got := d.Pool.Config().MaxConns; got != 30 {
		t.Fatalf("max conns = %d, want 30", got)
	}
	closeAll()
	closeAll() // a second close must not panic
}
