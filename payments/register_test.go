package payments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
)

func setup(t *testing.T) (*payments.Service, *apptest.App) {
	t.Helper()
	a := apptest.New(t, apptest.WithCurrencies(
		ledger.CurrencyConfig{Code: "usd", Ledger: 1, MinAmount: 1, Decimals: 2},
		ledger.CurrencyConfig{Code: "usdt", Ledger: 3, MinAmount: 1, Decimals: 6, Withdrawable: true}))
	s, err := payments.Register(context.Background(), a.App, payments.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return s, a
}

func TestSenderNeedsWithdrawable(t *testing.T) {
	s, _ := setup(t)
	if err := s.Sender("usd", &paymentstest.Sender{Method: "m"}, payments.WithdrawLimits{}); err == nil {
		t.Fatal("usd is not withdrawable")
	}
	if err := s.Sender("usdt", &paymentstest.Sender{Method: "m"}, payments.WithdrawLimits{}); err != nil {
		t.Fatal(err)
	}
}

func TestMethodNamesUnique(t *testing.T) {
	s, _ := setup(t)
	if err := s.Receiver("usdt", &paymentstest.Receiver{Method: "r"}, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Receiver("usdt", &paymentstest.Receiver{Method: "r"}, payments.DepositLimits{}); err == nil {
		t.Fatal("duplicate receiver")
	}
	if err := s.Receiver("usdt", &paymentstest.Receiver{}, payments.DepositLimits{}); err == nil {
		t.Fatal("empty name")
	}
	if err := s.Sender("usdt", &paymentstest.Sender{Method: "s"}, payments.WithdrawLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sender("usdt", &paymentstest.Sender{Method: "s"}, payments.WithdrawLimits{}); err == nil {
		t.Fatal("duplicate sender")
	}
}

func TestLimitsOverride(t *testing.T) {
	ctx := context.Background()
	s, a := setup(t)
	def := payments.WithdrawLimits{Min: 10, Fee: 1}
	if err := s.Sender("usdt", &paymentstest.Sender{Method: "s"}, def); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Limits(ctx, "usdt", "nope"); !errors.Is(err, payments.ErrUnknownMethod) {
		t.Fatalf("got %v", err)
	}
	if _, w, err := s.Limits(ctx, "usdt", "s"); err != nil || w != def {
		t.Fatalf("default: %v %v", w, err)
	}
	set := func(w payments.WithdrawLimits) error {
		tx, err := a.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := s.SetLimits(ctx, tx, "usdt", "s", nil, &w); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := set(payments.WithdrawLimits{Min: 50, Max: 10}); err == nil {
		t.Fatal("max < min accepted")
	}
	want := payments.WithdrawLimits{Min: 5, Max: 500, DailyCap: 1000}
	if err := set(want); err != nil {
		t.Fatal(err)
	}
	if _, w, err := s.Limits(ctx, "usdt", "s"); err != nil || w != want {
		t.Fatalf("override: %v %v", w, err)
	}
}

func TestZeroConfigDefaultsOnly(t *testing.T) {
	// a non-zero Lease/Tick is taken as given; Register must accept tiny values.
	a := apptest.New(t)
	if _, err := payments.Register(context.Background(), a.App, payments.Config{Lease: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
}

func TestOffOverride(t *testing.T) {
	ctx := context.Background()
	s, a := setup(t)
	if err := s.Receiver("usdt", &paymentstest.Receiver{Method: "r"}, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := s.SetLimits(ctx, tx, "usdt", "r", &payments.DepositLimits{Off: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if d, _, err := s.Limits(ctx, "usdt", "r"); err != nil || !d.Off {
		t.Fatalf("%v %v", d, err)
	}
}
