package ledger

import (
	"context"
	"errors"
	"testing"
)

// countingDriver accepts everything and counts the transfers it was sent.
type countingDriver struct{ transfers int }

func (d *countingDriver) CreateAccounts(context.Context, []Account) error { return nil }
func (d *countingDriver) CreateTransfers(_ context.Context, t []Transfer) ([]TransferResult, error) {
	d.transfers += len(t)
	return nil, nil
}
func (d *countingDriver) LookupAccounts(context.Context, []ID) ([]AccountBalance, error) {
	return nil, nil
}

func testLedger(t *testing.T) (*Ledger, *countingDriver) {
	d := &countingDriver{}
	l, err := New(d, CurrencyConfig{Code: "usd", Ledger: 1, MinAmount: 1})
	if err != nil {
		t.Fatal(err)
	}
	return l, d
}

// A game ID at or above 1<<24 would put bet transfer IDs into the namespaces of deposits, welcome
// grants and swaps (high 32 bits): every money method must refuse it before sending anything.
func TestGameRange(t *testing.T) {
	ctx := context.Background()
	for _, g := range []Game{0, MaxGame + 1, 1 << 40} {
		l, d := testLedger(t)
		for name, call := range map[string]func() error{
			"SettleInstant": func() error { return l.SettleInstant(ctx, g, "usd", 1, 1, 10, 0) },
			"Hold":          func() error { return l.Hold(ctx, g, "usd", 1, 1, 10) },
			"SettleHeld":    func() error { return l.SettleHeld(ctx, g, "usd", 1, 1, 10, 0) },
			"HoldExtra":     func() error { return l.HoldExtra(ctx, g, "usd", 1, 1, 1, 10) },
			"SettleHolds":   func() error { return l.SettleHolds(ctx, g, "usd", 1, 1, 10, nil, 0) },
			"HoldPot":       func() error { return l.HoldPot(ctx, g, "usd", PotEntry{ID: 1, UserID: 1, Amount: 10}) },
			"SettlePot": func() error {
				_, err := l.SettlePot(ctx, g, "usd", 1, []PotEntry{{ID: 1, UserID: 1, Amount: 10}}, nil)
				return err
			},
			"VoidPot": func() error { return l.VoidPot(ctx, g, "usd", []PotEntry{{ID: 1, UserID: 1, Amount: 10}}) },
			"SettlePool": func() error {
				_, err := l.SettlePool(ctx, g, "usd", 1, []PotEntry{{ID: 1, UserID: 1, Amount: 10}}, nil)
				return err
			},
		} {
			if err := call(); !errors.Is(err, ErrBadGame) {
				t.Errorf("game %d %s: err = %v, want ErrBadGame", g, name, err)
			}
		}
		if d.transfers != 0 {
			t.Fatalf("game %d: %d transfers sent", g, d.transfers)
		}
	}
}

func TestRegisterGame(t *testing.T) {
	l, _ := testLedger(t)
	if err := l.RegisterGame(1, "arena"); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterGame(1, "arena"); err != nil {
		t.Fatalf("registering the same game again: %v", err)
	}
	if err := l.RegisterGame(1, "runner"); err == nil {
		t.Fatal("a second game took ID 1")
	}
	if err := l.RegisterGame(FirstBrandGame, "arena"); err == nil {
		t.Fatal("arena took a second ID")
	}
	if err := l.RegisterGame(MaxGame+1, "big"); !errors.Is(err, ErrBadGame) {
		t.Fatalf("out of range: %v", err)
	}
	if err := l.RegisterGame(FirstBrandGame, "brand-crash"); err != nil {
		t.Fatal(err)
	}
}
