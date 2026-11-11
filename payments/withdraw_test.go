package payments_test

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
	"github.com/amir1430/gamify/realtime"
)

// withdrawSetup registers a usdt sender "wire" with l.
func withdrawSetup(t *testing.T, l payments.WithdrawLimits) (*payments.Service, *apptest.App) {
	t.Helper()
	s, a := setup(t)
	if err := s.Sender("usdt", &paymentstest.Sender{Method: "wire"}, l); err != nil {
		t.Fatal(err)
	}
	return s, a
}

func wd(a *apptest.App, u int64, key string, amount int64) (payments.Withdrawal, *realtime.ClientError) {
	return apptest.Call[payments.Withdrawal](a, u, "payments.withdraw", map[string]any{
		"currency": "usdt", "method": "wire", "amount": amount, "destination": "addr", "idem_key": key})
}

func mustWd(t *testing.T, a *apptest.App, u int64, key string, amount int64) payments.Withdrawal {
	t.Helper()
	w, cerr := wd(a, u, key, amount)
	if cerr != nil {
		t.Fatal(cerr)
	}
	return w
}

func wrows(t *testing.T, a *apptest.App) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pay_withdrawals`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func settled(t *testing.T, a *apptest.App, id int64) bool {
	t.Helper()
	var b bool
	if err := a.Pool.QueryRow(context.Background(), `SELECT settled FROM pay_withdrawals WHERE id = $1`, id).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWithdrawHolds(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{Fee: 10})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	if w.Status != "requested" || w.Fee != 10 || w.Amount != 400 || w.UserID != u || w.Destination != "addr" {
		t.Fatalf("%+v", w)
	}
	if b := a.Balance(u, "usdt"); b != 600 {
		t.Fatalf("balance %d", b)
	}
	if updates(a, u) != 1 {
		t.Fatalf("pushes %d", updates(a, u))
	}
}

func TestWithdrawIdempotent(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w1 := mustWd(t, a, u, "k", 400)
	w2 := mustWd(t, a, u, "k", 100)
	w3, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.withdraw", map[string]any{
		"currency": "usdt", "method": "nope", "amount": 1, "destination": "bad", "idem_key": "k"})
	if cerr != nil || w1.ID != w2.ID || w1.ID != w3.ID || w2.Amount != 400 {
		t.Fatalf("%+v %+v %+v %v", w1, w2, w3, cerr)
	}
	if b := a.Balance(u, "usdt"); b != 600 || wrows(t, a) != 1 {
		t.Fatalf("balance %d rows %d", b, wrows(t, a))
	}
	// Another user's same key is their own withdrawal.
	v := a.User("bob")
	a.Fund(v, "usdt", 100)
	if w := mustWd(t, a, v, "k", 50); w.ID == w1.ID {
		t.Fatal("key shared across users")
	}
}

func TestWithdrawValidation(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{Min: 5, Max: 500, Fee: 10})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	cases := []struct {
		name              string
		cur, method, dest string
		amount            int64
		code              uint32
	}{
		{"bad destination", "usdt", "wire", "bad", 100, payments.ErrBadDestination.Code},
		{"below min", "usdt", "wire", "addr", 3, payments.ErrLimit.Code},
		{"above max", "usdt", "wire", "addr", 501, payments.ErrLimit.Code},
		{"amount is fee", "usdt", "wire", "addr", 10, payments.ErrLimit.Code},
		{"unknown method", "usdt", "nope", "addr", 100, payments.ErrUnknownMethod.Code},
		{"not withdrawable", "usd", "wire", "addr", 100, payments.ErrUnknownMethod.Code},
	}
	for i, c := range cases {
		_, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.withdraw", map[string]any{
			"currency": c.cur, "method": c.method, "amount": c.amount, "destination": c.dest, "idem_key": string(rune('a' + i))})
		if cerr == nil || cerr.Code != c.code {
			t.Errorf("%s: got %v", c.name, cerr)
		}
	}
	if _, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.withdraw", map[string]any{
		"currency": "usdt", "method": "wire", "amount": 100, "destination": "addr"}); cerr == nil {
		t.Error("no idem_key accepted")
	}
	if b := a.Balance(u, "usdt"); b != 1000 || wrows(t, a) != 0 {
		t.Fatalf("balance %d rows %d", b, wrows(t, a))
	}
}

func TestWithdrawalsOff(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{Off: true})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	if _, cerr := wd(a, u, "k", 100); cerr == nil || cerr.Code != payments.ErrWithdrawalsOff.Code {
		t.Fatalf("got %v", cerr)
	}
	if wrows(t, a) != 0 {
		t.Fatal("row stored")
	}
}

func TestWithdrawInsufficient(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{})
	u := a.User("ann")
	a.Fund(u, "usdt", 100)
	if _, cerr := wd(a, u, "k", 400); cerr == nil || cerr.Code != payments.ErrInsufficient.Code {
		t.Fatalf("got %v", cerr)
	}
	var id int64
	var status string
	if err := a.Pool.QueryRow(context.Background(), `SELECT id, status FROM pay_withdrawals`).Scan(&id, &status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !settled(t, a, id) || a.Balance(u, "usdt") != 100 {
		t.Fatalf("status %s settled %v balance %d", status, settled(t, a, id), a.Balance(u, "usdt"))
	}
	// A refused hold is never voided: the reconciler leaves it alone.
	pushes := updates(a, u)
	a.ReconcileNow()
	if updates(a, u) != pushes || a.Balance(u, "usdt") != 100 {
		t.Fatalf("reconciler touched a refused hold: pushes %d -> %d", pushes, updates(a, u))
	}
	a.Fund(u, "usdt", 1000)
	if w := mustWd(t, a, u, "k2", 400); w.Status != "requested" {
		t.Fatalf("%+v", w)
	}
	if b := a.Balance(u, "usdt"); b != 700 {
		t.Fatalf("balance %d", b)
	}
}

func TestDailyCapConcurrent(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{DailyCap: 500})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	var wg sync.WaitGroup
	errs := make([]*realtime.ClientError, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = wd(a, u, string(rune('a'+i)), 300)
		}()
	}
	wg.Wait()
	ok, capped := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case e.Code == payments.ErrDailyCap.Code:
			capped++
		default:
			t.Fatal(e)
		}
	}
	if ok != 1 || capped != 1 || a.Balance(u, "usdt") != 700 || wrows(t, a) != 1 {
		t.Fatalf("ok %d capped %d balance %d rows %d", ok, capped, a.Balance(u, "usdt"), wrows(t, a))
	}
	if w := mustWd(t, a, u, "c", 200); w.Status != "requested" { // exactly up to the cap
		t.Fatalf("%+v", w)
	}
	// sum + amount would overflow int64 and pass the cap.
	if _, e := wd(a, u, "d", math.MaxInt64-100); e == nil || e.Code != payments.ErrDailyCap.Code {
		t.Fatalf("huge amount: %v", e)
	}
}

func TestAutoApprove(t *testing.T) {
	_, a := withdrawSetup(t, payments.WithdrawLimits{AutoApproveUpTo: 100})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	if w := mustWd(t, a, u, "a", 100); w.Status != "approved" {
		t.Fatalf("%+v", w)
	}
	if w := mustWd(t, a, u, "b", 101); w.Status != "requested" {
		t.Fatalf("%+v", w)
	}
}

func TestCancel(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{AutoApproveUpTo: 50})
	u, v := a.User("ann"), a.User("bob")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "a", 400)
	if _, cerr := apptest.Call[payments.Withdrawal](a, v, "payments.cancel", map[string]any{"id": w.ID}); cerr == nil || cerr.Code != payments.ErrNotFound.Code {
		t.Fatalf("other user: %v", cerr)
	}
	c, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.cancel", map[string]any{"id": w.ID})
	if cerr != nil || c.Status != "cancelled" {
		t.Fatalf("%+v %v", c, cerr)
	}
	if b := a.Balance(u, "usdt"); b != 1000 || !settled(t, a, w.ID) {
		t.Fatalf("balance %d settled %v", b, settled(t, a, w.ID))
	}
	if _, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.cancel", map[string]any{"id": w.ID}); cerr == nil || cerr.Code != payments.ErrNotCancellable.Code {
		t.Fatalf("twice: %v", cerr)
	}
	ap := mustWd(t, a, u, "b", 50)
	if _, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.cancel", map[string]any{"id": ap.ID}); cerr == nil || cerr.Code != payments.ErrNotCancellable.Code {
		t.Fatalf("approved: %v", cerr)
	}
	if g, err := s.Get(context.Background(), w.ID); err != nil || g.Status != "cancelled" {
		t.Fatalf("%+v %v", g, err)
	}
}

func TestReconcileWithdrawals(t *testing.T) {
	ctx := context.Background()
	s, a := withdrawSetup(t, payments.WithdrawLimits{})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)

	// A crash between the row and its hold: the reconciler holds and moves it on.
	id, err := payments.LeaveHolding(ctx, s, u, "usdt", "wire", "h1", 300)
	if err != nil {
		t.Fatal(err)
	}
	a.ReconcileNow()
	if w, _ := s.Get(ctx, id); w.Status != "requested" || a.Balance(u, "usdt") != 700 {
		t.Fatalf("%+v balance %d", w, a.Balance(u, "usdt"))
	}

	// The user's retry finishes it too, without holding twice.
	id2, err := payments.LeaveHolding(ctx, s, u, "usdt", "wire", "h2", 200)
	if err != nil {
		t.Fatal(err)
	}
	if w := mustWd(t, a, u, "h2", 999); w.ID != id2 || w.Status != "requested" || a.Balance(u, "usdt") != 500 {
		t.Fatalf("%+v balance %d", w, a.Balance(u, "usdt"))
	}

	// A cancel whose release never ran: the reconciler releases it.
	if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET status = 'cancelled' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	a.ReconcileNow()
	if a.Balance(u, "usdt") != 800 || !settled(t, a, id) {
		t.Fatalf("balance %d settled %v", a.Balance(u, "usdt"), settled(t, a, id))
	}
	a.ReconcileNow()
	if a.Balance(u, "usdt") != 800 {
		t.Fatal("released twice")
	}
}
