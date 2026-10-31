package fairness

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

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

// First bets of players without seed pairs, more at once than the pool has connections, must not
// deadlock: the pairs are made in the bet's own transaction, never on a second connection. And one
// player's concurrent first bets share one pair, each with its own nonce.
func TestFirstBetsDontStarvePool(t *testing.T) {
	s, _ := service(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := s.pool.Config().Copy()
	cfg.MaxConns = 2
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	s = NewService(small, s.sealer)
	newUser := func() int64 {
		var id int64
		if err := small.QueryRow(ctx, `INSERT INTO users DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	bet := func(userID int64) (Draw, error) {
		var d Draw
		err := pgx.BeginFunc(ctx, small, func(tx pgx.Tx) error {
			var err error
			d, err = s.Next(ctx, tx, userID, 0)
			return err
		})
		return d, err
	}
	users := make([]int64, 8)
	for i := range users {
		users[i] = newUser()
	}
	same := newUser()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	nonces := make(chan Draw, 4)
	for _, u := range users {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := bet(u); errs <- err }()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := bet(same)
			errs <- err
			nonces <- d
		}()
	}
	wg.Wait()
	close(errs)
	close(nonces)
	for err := range errs {
		if err != nil {
			t.Fatalf("first bet: %v", err)
		}
	}
	seen := map[int64]bool{}
	var pair int64
	for d := range nonces {
		if seen[d.Nonce] || (pair != 0 && d.SeedPairID != pair) {
			t.Fatalf("one player's first bets: nonce %d pair %d (seen %v, pair %d)", d.Nonce, d.SeedPairID, seen, pair)
		}
		seen[d.Nonce], pair = true, d.SeedPairID
	}
	info, err := s.Info(ctx, same)
	if err != nil || info.Nonce != 4 || info.NextServerSeedHash == "" {
		t.Fatalf("after 4 bets: %+v %v", info, err)
	}
}
