package dice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/ledgertest"
	"github.com/amir1430/gamify/ledger/postgres"
	"github.com/amir1430/gamify/ledger/tigerbeetle"

	"github.com/jackc/pgx/v5/pgxpool"
	tb "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

// Runs against real Postgres (TEST_DATABASE_URL), once per ledger driver: Postgres, and
// TigerBeetle when TEST_TB_ADDRESS is set. The game doesn't know which one it runs on.
//
//	TEST_DATABASE_URL=postgres://... TEST_TB_ADDRESS=127.0.0.1:3000 go test ./games/dice -run Integration
func TestIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	must(t, err)
	defer pool.Close()
	must(t, db.Migrate(ctx, pool, db.Migrations, "goose_platform"))
	must(t, db.Migrate(ctx, pool, fairness.Migrations, "goose_fairness"))
	must(t, db.Migrate(ctx, pool, postgres.Migrations, "goose_ledger"))
	must(t, db.Migrate(ctx, pool, migrations, "goose_dice"))

	t.Run("postgres", func(t *testing.T) { play(t, pool, postgres.New(pool)) })
	t.Run("tigerbeetle", func(t *testing.T) {
		addr := os.Getenv("TEST_TB_ADDRESS")
		if addr == "" {
			t.Skip("TEST_TB_ADDRESS not set")
		}
		client, err := tb.NewClient(types.ToUint128(0), []string{addr})
		must(t, err)
		defer client.Close()
		play(t, pool, tigerbeetle.New(client))
	})
}

func play(t *testing.T, pool *pgxpool.Pool, d ledger.Driver) {
	ctx := context.Background()
	wallet, err := ledger.New(d, ledgertest.USD, ledgertest.Tickets)
	must(t, err)
	must(t, wallet.Init(ctx))
	sealer, err := fairness.NewSealer("k1", bytes.Repeat([]byte{1}, 32))
	must(t, err)
	fair := fairness.NewService(pool, sealer)
	svc := NewService(pool, fair, wallet, gameconfig.NewService(pool, wallet))

	var userID int64
	must(t, pool.QueryRow(ctx, `INSERT INTO users DEFAULT VALUES RETURNING id`).Scan(&userID))
	must(t, wallet.EnsureUser(ctx, userID))
	must(t, fair.Ensure(ctx, userID))
	must(t, fair.Ensure(ctx, userID)) // idempotent

	balance := func() int64 {
		b, err := wallet.Balances(ctx, userID)
		must(t, err)
		return b["usd"]
	}
	req := func(key string) BetRequest {
		return BetRequest{Amount: 100, Currency: "usd", Target: 5000, Over: true, IdemKey: key}
	}

	// No money: rejected by the ledger, bet voided.
	if _, err := svc.Bet(ctx, userID, req("broke")); !errors.Is(err, errInsufficient) {
		t.Fatalf("want insufficient, got %v", err)
	}

	must(t, wallet.TopUp(ctx, "usd", userID, 10_000))
	res, err := svc.Bet(ctx, userID, req("first"))
	must(t, err)
	if got, want := balance(), 10_000-100+res.Payout; got != want {
		t.Fatalf("balance %d, want %d", got, want)
	}

	// Same key again: same bet, no second charge.
	again, err := svc.Bet(ctx, userID, req("first"))
	must(t, err)
	if again != res || balance() != 10_000-100+res.Payout {
		t.Fatalf("retry changed things: %+v vs %+v", again, res)
	}

	// Concurrent bets: unique nonces, balance adds up exactly.
	before := balance()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var delta int64
	nonces := map[int64]bool{}
	for i := range 20 {
		wg.Go(func() {
			r, err := svc.Bet(ctx, userID, req(fmt.Sprintf("c%d", i)))
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			delta += r.Payout - r.Amount
			if nonces[r.Nonce] {
				t.Errorf("nonce %d reused", r.Nonce)
			}
			nonces[r.Nonce] = true
		})
	}
	wg.Wait()
	if got := balance(); got != before+delta {
		t.Fatalf("balance %d, want %d", got, before+delta)
	}

	// Reconciler finishes a bet stuck in pending (e.g. crash before the ledger call).
	stuck, err := svc.create(ctx, userID, req("stuck"))
	must(t, err)
	before = balance()
	_, err = pool.Exec(ctx, `UPDATE dice_bets SET created_at = now() - interval '1 minute' WHERE id = $1`, stuck.ID)
	must(t, err)
	svc.Reconcile(ctx)
	var status string
	must(t, pool.QueryRow(ctx, `SELECT status FROM dice_bets WHERE id = $1`, stuck.ID).Scan(&status))
	if status != "settled" || balance() != before-stuck.Amount+stuck.Payout {
		t.Fatalf("reconcile: status %s balance %d", status, balance())
	}

	// Tickets: the welcome grant (once, however often the user logs in) pays for a tickets bet; USD doesn't move.
	must(t, wallet.EnsureUser(ctx, userID))
	tickets := func() int64 {
		b, err := wallet.Balances(ctx, userID)
		must(t, err)
		return b["tickets"]
	}
	if tickets() != ledgertest.Tickets.Welcome {
		t.Fatalf("welcome tickets %d", tickets())
	}
	before = balance()
	tix, err := svc.Bet(ctx, userID, BetRequest{Amount: 100, Currency: "tickets", Target: 5000, Over: true, IdemKey: "tix"})
	must(t, err)
	if tix.Currency != "tickets" || tickets() != ledgertest.Tickets.Welcome-100+tix.Payout || balance() != before {
		t.Fatalf("tickets bet: %+v, tickets %d, usd %d (was %d)", tix, tickets(), balance(), before)
	}
	if _, err := svc.Bet(ctx, userID, BetRequest{Amount: 9, Currency: "tickets", Target: 5000, Over: true, IdemKey: "tix-small"}); !errors.Is(err, errBadRequest) {
		t.Fatalf("9 tickets: want invalid bet, got %v", err)
	}

	// Demo (amount 0): played and recorded on the seed pair, no money moves, win read from the roll.
	before, tixBefore := balance(), tickets()
	for i := range 5 {
		d, err := svc.Bet(ctx, userID, BetRequest{Amount: 0, Target: 5000, Over: true, IdemKey: fmt.Sprintf("demo%d", i)})
		must(t, err)
		if d.Payout != 0 || d.Win != (d.Roll >= 5000) || d.Currency != "usd" {
			t.Fatalf("demo: %+v", d)
		}
	}
	if balance() != before || tickets() != tixBefore {
		t.Fatalf("demo moved money: usd %d (was %d), tickets %d (was %d)", balance(), before, tickets(), tixBefore)
	}

	// Provably fair: after rotation the revealed seed reproduces the first bet's roll.
	info, err := fair.Info(ctx, userID)
	must(t, err)
	rev, err := fair.Rotate(ctx, userID, "my-seed")
	must(t, err)
	key := []byte(rev.ServerSeed) // the revealed hex text is the HMAC key
	if rev.ServerSeedHash != info.ServerSeedHash || rev.ClientSeed != info.ClientSeed {
		t.Fatal("revealed pair doesn't match the committed one")
	}
	if got := Roll(fairness.Hash(key, rev.ClientSeed, res.Nonce)); got != res.Roll {
		t.Fatalf("verify roll %d, bet roll %d", got, res.Roll)
	}
	after, err := fair.Info(ctx, userID)
	must(t, err)
	if after.ServerSeedHash != info.NextServerSeedHash || after.ClientSeed != "my-seed" || after.Nonce != 0 {
		t.Fatalf("rotation: %+v (was next %s)", after, info.NextServerSeedHash)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
