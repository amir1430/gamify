package payments_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
	"github.com/amir1430/gamify/realtime"
	"github.com/jackc/pgx/v5"
)

// inTx runs f in a transaction and commits if it succeeds.
func inTx(t *testing.T, a *apptest.App, f func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func approve(t *testing.T, s *payments.Service, a *apptest.App, id, admin int64) (w payments.Withdrawal, err error) {
	err = inTx(t, a, func(tx pgx.Tx) (e error) { w, e = s.Approve(context.Background(), tx, id, admin); return })
	return
}

func reject(t *testing.T, s *payments.Service, a *apptest.App, id, admin int64) (w payments.Withdrawal, err error) {
	err = inTx(t, a, func(tx pgx.Tx) (e error) { w, e = s.Reject(context.Background(), tx, id, admin, "no"); return })
	return
}

func resolve(t *testing.T, s *payments.Service, a *apptest.App, id int64, r payments.Result) (w payments.Withdrawal, err error) {
	err = inTx(t, a, func(tx pgx.Tx) (e error) { w, e = s.Resolve(context.Background(), tx, id, 9, r); return })
	return
}

func TestHistoryOwnOnly(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{})
	r := &paymentstest.Receiver{Method: "chain"}
	if err := s.Receiver("usdt", r, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx, payments.FakeHost(s, "chain"))
	for !r.Ready() {
	}
	ann, bob := a.User("ann"), a.User("bob")
	a.Fund(ann, "usdt", 1000)
	a.Fund(bob, "usdt", 1000)
	dep(t, a, ann, 0)
	dep(t, a, bob, 0)
	for _, in := range []payments.Incoming{
		{ExternalID: "1", Address: "addr-" + strconv.FormatInt(ann, 10), Currency: "usdt", Amount: 5},
		{ExternalID: "2", Address: "addr-" + strconv.FormatInt(bob, 10), Currency: "usdt", Amount: 7}} {
		if err := r.Pay(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	wa := mustWd(t, a, ann, "k", 100)
	wb := mustWd(t, a, bob, "k", 200)
	type hist struct {
		Deposits    []payments.Deposit
		Withdrawals []payments.Withdrawal
	}
	h, cerr := apptest.Call[hist](a, ann, "payments.history", map[string]any{})
	if cerr != nil || len(h.Deposits) != 1 || h.Deposits[0].Amount != 5 || len(h.Withdrawals) != 1 || h.Withdrawals[0].ID != wa.ID {
		t.Fatalf("%+v %v", h, cerr)
	}
	// A user_id in the payload is ignored: identity comes from the connection.
	h, _ = apptest.Call[hist](a, bob, "payments.history", map[string]any{"user_id": ann})
	if len(h.Deposits) != 1 || h.Deposits[0].UserID != bob || len(h.Withdrawals) != 1 || h.Withdrawals[0].ID != wb.ID {
		t.Fatalf("%+v", h)
	}
	if h, _ = apptest.Call[hist](a, ann, "payments.history", map[string]any{"kind": "deposit"}); len(h.Withdrawals) != 0 || len(h.Deposits) != 1 {
		t.Fatalf("%+v", h)
	}
	for _, bad := range []map[string]any{{"limit": 101}, {"limit": -1}, {"kind": "x"}} {
		if _, cerr := apptest.Call[hist](a, ann, "payments.history", bad); cerr == nil || cerr.Code != realtime.CodeBadPayload {
			t.Fatalf("%v: %v", bad, cerr)
		}
	}
	// The admin's activity feed: all users, newest first, paged by the last ID.
	src := a.HistorySources()["payments"]
	acts, err := src(ctx, 0, 0, 10)
	if err != nil || len(acts) != 4 {
		t.Fatalf("%+v %v", acts, err)
	}
	for i := 1; i < len(acts); i++ {
		if acts[i].At.After(acts[i-1].At) {
			t.Fatalf("not newest first: %+v", acts)
		}
	}
	var seen int
	for before := int64(0); ; seen++ {
		page, err := src(ctx, 0, before, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		before = page[0].ID
	}
	if seen != 4 {
		t.Fatalf("paged %d", seen)
	}
	own, _ := src(ctx, ann, 0, 10)
	for _, x := range own {
		if x.UserID != ann || x.Game != "payments" {
			t.Fatalf("%+v", x)
		}
		if x.Amount == 0 == (x.Payout == 0) {
			t.Fatalf("amount/payout %+v", x)
		}
	}
}

func TestWithdrawalsFilter(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{})
	ctx := context.Background()
	ann, bob := a.User("ann"), a.User("bob")
	a.Fund(ann, "usdt", 1000)
	a.Fund(bob, "usdt", 1000)
	w1 := mustWd(t, a, ann, "1", 10)
	w2 := mustWd(t, a, bob, "2", 20)
	w3 := mustWd(t, a, ann, "3", 30)
	if _, err := reject(t, s, a, w2.ID, 1); err != nil {
		t.Fatal(err)
	}
	ids := func(f payments.Filter, before int64, limit int32) (out []int64) {
		ws, err := s.Withdrawals(ctx, f, before, limit)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws {
			out = append(out, w.ID)
		}
		return
	}
	eq := func(got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(ids(payments.Filter{}, 0, 10), w3.ID, w2.ID, w1.ID)
	eq(ids(payments.Filter{Status: "rejected"}, 0, 10), w2.ID)
	eq(ids(payments.Filter{UserID: ann}, 0, 10), w3.ID, w1.ID)
	eq(ids(payments.Filter{Currency: "usdt", Method: "wire"}, 0, 10), w3.ID, w2.ID, w1.ID)
	eq(ids(payments.Filter{Currency: "usd"}, 0, 10))
	eq(ids(payments.Filter{}, w3.ID, 1), w2.ID)
	eq(ids(payments.Filter{}, w2.ID, 5), w1.ID)
}

func TestApproveTwoPeople(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{SecondApprovalAbove: 100})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	small, big := mustWd(t, a, u, "s", 50), mustWd(t, a, u, "b", 500)
	if w, err := approve(t, s, a, small.ID, 1); err != nil || w.Status != "approved" {
		t.Fatalf("%+v %v", w, err)
	}
	if w, err := approve(t, s, a, big.ID, 1); err != nil || w.Status != "requested" {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := approve(t, s, a, big.ID, 1); !errors.Is(err, payments.ErrSameApprover) || payments.ErrSameApprover.Code != 1048 {
		t.Fatalf("%v", err)
	}
	if w, err := approve(t, s, a, big.ID, 2); err != nil || w.Status != "approved" {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := approve(t, s, a, big.ID, 3); err == nil {
		t.Fatal("approved twice")
	}
	if _, err := approve(t, s, a, 99999, 1); !errors.Is(err, payments.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	var by int64
	a.Pool.QueryRow(context.Background(), `SELECT decided_by FROM pay_withdrawals WHERE id = $1`, big.ID).Scan(&by)
	if by != 2 {
		t.Fatalf("decided_by %d", by)
	}
}

func TestApproveConcurrentFirst(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{SecondApprovalAbove: 100})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "b", 500)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = approve(t, s, a, w.ID, 1) // the same admin twice at once
		}()
	}
	wg.Wait()
	ok, same := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, payments.ErrSameApprover):
			same++
		}
	}
	if ok != 1 || same != 1 {
		t.Fatalf("%v", errs)
	}
	if g := status(t, s, w.ID); g.Status != "requested" {
		t.Fatalf("%+v", g)
	}
}

func TestRejectRefunds(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	if b := a.Balance(u, "usdt"); b != 600 {
		t.Fatalf("balance %d", b)
	}
	got, err := reject(t, s, a, w.ID, 1)
	if err != nil || got.Status != "rejected" || got.Reason != "no" {
		t.Fatalf("%+v %v", got, err)
	}
	s.Advance(context.Background(), w.ID)
	if b := a.Balance(u, "usdt"); b != 1000 {
		t.Fatalf("balance %d", b)
	}
	if g := status(t, s, w.ID); !g.Settled {
		t.Fatalf("%+v", g)
	}
	if _, err := reject(t, s, a, w.ID, 1); err == nil {
		t.Fatal("rejected twice")
	}
}

func TestRejectApprovedBeforeSend(t *testing.T) {
	snd := &countSender{}
	s, a := sendSetup(t, payments.Config{Tick: time.Hour}, snd, 0)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	if _, err := approve(t, s, a, w.ID, 1); err != nil {
		t.Fatal(err)
	}
	if g, err := reject(t, s, a, w.ID, 1); err != nil || g.Status != "rejected" {
		t.Fatalf("%+v %v", g, err)
	}
	s.Advance(context.Background(), w.ID)
	if len(snd.Calls()) != 0 || a.Balance(u, "usdt") != 1000 {
		t.Fatalf("calls %v balance %d", snd.Calls(), a.Balance(u, "usdt"))
	}
}

func TestRejectSendingRefused(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{})
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	if _, err := a.Pool.Exec(context.Background(), `UPDATE pay_withdrawals SET status = 'sending', lease_until = now() + interval '1 hour' WHERE id = $1`, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reject(t, s, a, w.ID, 1); err == nil {
		t.Fatal("rejected a sending withdrawal")
	}
	s.Advance(context.Background(), w.ID) // must not void either
	g := status(t, s, w.ID)
	if g.Status != "sending" || g.Settled || a.Balance(u, "usdt") != 600 {
		t.Fatalf("%+v balance %d", g, a.Balance(u, "usdt"))
	}
}

func TestResolveOnlySending(t *testing.T) {
	s, a := withdrawSetup(t, payments.WithdrawLimits{Fee: 10})
	u := a.User("ann")
	a.Fund(u, "usdt", 3000)
	req := mustWd(t, a, u, "r", 100)
	if _, err := resolve(t, s, a, req.ID, payments.Result{State: payments.StatePaid, Ref: "tx"}); err == nil {
		t.Fatal("resolved a requested withdrawal")
	}
	mk := func(key string) int64 {
		w := mustWd(t, a, u, key, 100)
		if _, err := a.Pool.Exec(context.Background(), `UPDATE pay_withdrawals SET status = 'sending' WHERE id = $1`, w.ID); err != nil {
			t.Fatal(err)
		}
		return w.ID
	}
	id := mk("p")
	for _, bad := range []payments.Result{{State: payments.StatePaid}, {State: payments.StateFailed},
		{State: payments.StatePending, Ref: "x", Reason: "y"}, {State: payments.StateUnknown}} {
		if _, err := resolve(t, s, a, id, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if g, err := resolve(t, s, a, id, payments.Result{State: payments.StatePaid, Ref: "tx1"}); err != nil || g.Status != "paid" || g.Ref != "tx1" {
		t.Fatalf("%+v %v", g, err)
	}
	s.Advance(context.Background(), id)
	if g := status(t, s, id); !g.Settled {
		t.Fatalf("%+v", g)
	}
	if _, err := resolve(t, s, a, id, payments.Result{State: payments.StateFailed, Reason: "x"}); err == nil {
		t.Fatal("resolved twice")
	}
	id = mk("f")
	before := a.Balance(u, "usdt")
	if g, err := resolve(t, s, a, id, payments.Result{State: payments.StateFailed, Reason: "bounced"}); err != nil || g.Status != "failed" || g.Reason != "bounced" {
		t.Fatalf("%+v %v", g, err)
	}
	s.Advance(context.Background(), id)
	if a.Balance(u, "usdt") != before+100 {
		t.Fatalf("not refunded: %d", a.Balance(u, "usdt"))
	}
}
