package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/ledgertest"
	"github.com/amir1430/gamify/ledger/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a real Postgres (TEST_DATABASE_URL).
func TestIntegrationConformance(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, postgres.Migrations, "goose_ledger"); err != nil {
		t.Fatal(err)
	}
	ledgertest.Run(t, func(*testing.T) ledger.Driver { return postgres.New(pool) })
}
