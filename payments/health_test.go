package payments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
)

func TestCount(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	u := a.User("ann")
	for i, st := range []string{"requested", "requested", "sending", "paid"} {
		id, err := payments.LeaveHolding(ctx, s, u, "usdt", "wire", string(rune('a'+i)), 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET status = $2 WHERE id = $1`, id, st); err != nil {
			t.Fatal(err)
		}
		if st == "sending" { // requested two hours ago
			if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET created_at = now() - interval '2 hours' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, tc := range map[string]struct {
		f    payments.Filter
		want int64
	}{
		"all":              {payments.Filter{}, 4},
		"requested":        {payments.Filter{Status: "requested"}, 2},
		"any of":           {payments.Filter{Statuses: []string{"approved", "sending", "paid"}}, 2},
		"old open":         {payments.Filter{Statuses: []string{"approved", "sending"}, Until: time.Now().Add(-time.Hour)}, 1},
		"recent requested": {payments.Filter{Status: "requested", Since: time.Now().Add(-time.Hour)}, 2},
		"other user":       {payments.Filter{UserID: u + 1}, 0},
	} {
		n, err := s.CountWithdrawals(ctx, tc.f)
		if err != nil || n != tc.want {
			t.Errorf("%s: %d, %v; want %d", name, n, err, tc.want)
		}
	}
	// Statuses filters the lists too.
	ws, err := s.Withdrawals(ctx, payments.Filter{Statuses: []string{"sending", "paid"}}, 0, 10)
	if err != nil || len(ws) != 2 {
		t.Fatalf("list by statuses: %d, %v", len(ws), err)
	}

	// An unmatched deposit counts; the recent window leaves an old one out.
	r := &paymentstest.Receiver{Method: "chain"}
	if err := s.Receiver("usdt", r, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	h := payments.FakeHost(s, "chain")
	for _, ext := range []string{"x1", "x2"} {
		if err := h.Received(ctx, payments.Incoming{ExternalID: ext, Address: "nobody", Currency: "usdt", Amount: 5}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Pool.Exec(ctx, `UPDATE pay_deposits SET created_at = now() - interval '3 days' WHERE external_id = 'x1'`); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountDeposits(ctx, payments.Filter{Status: "unmatched", Since: time.Now().Add(-24 * time.Hour)})
	if err != nil || n != 1 {
		t.Fatalf("unmatched in 24 h: %d, %v", n, err)
	}
}

// failRun is a Receiver whose Run fails at once.
type failRun struct{ paymentstest.Receiver }

func (f *failRun) Run(context.Context, *payments.Host) error {
	return errors.New("chain node unreachable")
}

func TestHealth(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	snd := &paymentstest.Sender{Method: "wire"}
	if err := s.Sender("usdt", snd, payments.WithdrawLimits{}); err != nil {
		t.Fatal(err)
	}
	rcv := &failRun{paymentstest.Receiver{Method: "chain"}}
	if err := s.Receiver("usdt", rcv, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Receiver("usd", rcv, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	get := func(name, kind string) payments.MethodHealth {
		t.Helper()
		for _, h := range s.Health() {
			if h.Name == name && h.Kind == kind {
				return h
			}
		}
		t.Fatalf("no health for %s %s in %v", name, kind, s.Health())
		return payments.MethodHealth{}
	}
	if h := get("chain", "deposit"); h.Running || h.Restarts != 0 || h.LastError != "" ||
		len(h.Currencies) != 2 || h.Currencies[0] != "usd" || h.Currencies[1] != "usdt" {
		t.Fatalf("chain before Run = %+v", h)
	}

	if err := payments.RunReceiver(ctx, s, "chain"); err == nil {
		t.Fatal("Run's error was lost")
	}
	if h := get("chain", "deposit"); h.Running || h.Restarts != 1 || h.LastError != "chain node unreachable" || h.LastErrorAt.IsZero() {
		t.Fatalf("chain after a failed Run = %+v", h)
	}

	// A send error is the sender's last error; a later answer is its last success.
	u := a.User("ann")
	a.Fund(u, "usdt", 1_000)
	snd.SetErr(errors.New("bank says no"))
	w, cerr := apptest.Call[payments.Withdrawal](a, u, "payments.withdraw", map[string]any{
		"currency": "usdt", "method": "wire", "amount": 100, "destination": "addr", "idem_key": "k1"})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET status = 'approved' WHERE id = $1`, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := payments.Advance(ctx, s, w.ID); err == nil {
		t.Fatal("the send error was lost")
	}
	h := get("wire", "withdraw")
	if h.LastError != "bank says no" || h.LastErrorAt.IsZero() || !h.LastOK.IsZero() {
		t.Fatalf("wire after an error = %+v", h)
	}
	snd.SetErr(nil)
	if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET lease_until = 'epoch' WHERE id = $1`, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := payments.Advance(ctx, s, w.ID); err != nil {
		t.Fatal(err)
	}
	if h := get("wire", "withdraw"); h.LastOK.IsZero() || h.LastError != "bank says no" {
		t.Fatalf("wire after a success = %+v (the last error stays, with its time)", h)
	}
}
