package fairness

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/amir1430/gamify/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a real Postgres (TEST_DATABASE_URL): the platform's tables, then the module's.
func service(t *testing.T) (*Service, int64) {
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
	if err := db.Migrate(ctx, pool, db.Migrations, "goose_platform"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool, Migrations, "goose_fairness"); err != nil {
		t.Fatal(err)
	}
	sealer, err := NewSealer("k1", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users DEFAULT VALUES RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return NewService(pool, sealer), userID
}

// A player must be able to see the seed hash before their first bet: Info commits the seeds.
func TestInfoCommitsBeforeFirstBet(t *testing.T) {
	s, userID := service(t)
	info, err := s.Info(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerSeedHash == "" || info.NextServerSeedHash == "" || info.Nonce != 0 {
		t.Fatalf("no commitment before the first bet: %+v", info)
	}
	again, err := s.Info(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if again != info {
		t.Fatalf("Info changed the seeds: %+v then %+v", info, again)
	}
}

func TestGuardVetoesRotation(t *testing.T) {
	s, userID := service(t)
	ctx := context.Background()
	veto := errors.New("live game")
	s.Guard(func(context.Context, pgx.Tx, int64) error { return veto })
	if _, err := s.Info(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(ctx, userID, "mine"); !errors.Is(err, veto) {
		t.Fatalf("rotate = %v, want the guard's veto", err)
	}
}
