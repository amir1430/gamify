package payments_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
	"github.com/amir1430/gamify/realtime"
)

// countSender is the fake Sender that also records every Send call (the fake lists distinct payouts
// only), after an optional delay.
type countSender struct {
	*paymentstest.Sender
	delay time.Duration
	gate  chan struct{} // Send waits for it to close

	mu    sync.Mutex
	calls []int64
}

func (c *countSender) Send(ctx context.Context, p payments.Payout) (payments.Result, error) {
	time.Sleep(c.delay)
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	c.calls = append(c.calls, p.ID)
	c.mu.Unlock()
	return c.Sender.Send(ctx, p)
}

func (c *countSender) Calls() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// sendSetup registers snd as usdt sender "wire", fee 10, auto-approving up to auto.
func sendSetup(t *testing.T, cfg payments.Config, snd *countSender, auto int64) (*payments.Service, *apptest.App) {
	t.Helper()
	a := apptest.New(t, apptest.WithCurrencies(
		ledger.CurrencyConfig{Code: "usdt", Ledger: 3, MinAmount: 1, Decimals: 6, Withdrawable: true}))
	s, err := payments.Register(context.Background(), a.App, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { payments.WaitSends(s) })
	if snd.Sender == nil {
		snd.Sender = &paymentstest.Sender{}
	}
	snd.Method = "wire"
	if err := s.Sender("usdt", snd, payments.WithdrawLimits{Fee: 10, AutoApproveUpTo: auto}); err != nil {
		t.Fatal(err)
	}
	return s, a
}

func status(t *testing.T, s *payments.Service, id int64) payments.Withdrawal {
	t.Helper()
	w, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// pushed is the statuses of the user's withdrawal pushes, in order.
func pushed(a *apptest.App, u int64) []string {
	var out []string
	for _, p := range a.Pushes(realtime.UserChannel(u)) {
		var m struct {
			Type       string
			Withdrawal *payments.Withdrawal
		}
		if json.Unmarshal(p, &m) == nil && m.Type == "payments.updated" && m.Withdrawal != nil {
			out = append(out, m.Withdrawal.Status)
		}
	}
	return out
}

func system(t *testing.T, a *apptest.App) map[string]int64 {
	t.Helper()
	b, err := a.Ledger.SystemBalances(context.Background(), "usdt")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reconcile runs the reconciler once the 1 ms lease has expired.
func reconcile(a *apptest.App) {
	time.Sleep(5 * time.Millisecond)
	a.ReconcileNow()
}

var shortLease = payments.Config{Lease: time.Millisecond, Tick: time.Hour}

func TestSendPaid(t *testing.T) {
	snd := &countSender{}
	s, a := sendSetup(t, shortLease, snd, 1000)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	before := system(t, a)
	w := mustWd(t, a, u, "k", 400)
	payments.WaitSends(s)
	g := status(t, s, w.ID)
	if g.Status != "paid" || g.Ref != "ref-"+strconv.FormatInt(w.ID, 10) || !settled(t, a, w.ID) {
		t.Fatalf("%+v settled %v", g, settled(t, a, w.ID))
	}
	if b := a.Balance(u, "usdt"); b != 600 {
		t.Fatalf("balance %d", b)
	}
	after := system(t, a)
	if d := after["outflow"] - before["outflow"]; d != 390 {
		t.Fatalf("outflow +%d", d)
	}
	if d := after["house"] - before["house"]; d != 10 {
		t.Fatalf("house +%d", d)
	}
	if sent := snd.SentPayouts(); len(sent) != 1 || sent[0].Amount != 390 || sent[0].ID != w.ID || sent[0].Destination != "addr" {
		t.Fatalf("sent %+v", sent)
	}
	// One push per status change, and one when the money settles.
	if p := pushed(a, u); !slices.Equal(p, []string{"approved", "sending", "paid", "paid"}) {
		t.Fatalf("pushes %v", p)
	}
	// Done: the reconciler neither sends nor posts again.
	reconcile(a)
	if len(snd.Calls()) != 1 || a.Balance(u, "usdt") != 600 || len(pushed(a, u)) != 4 {
		t.Fatalf("calls %v balance %d", snd.Calls(), a.Balance(u, "usdt"))
	}
}

func TestWithdrawReturnsBeforeSend(t *testing.T) {
	snd := &countSender{gate: make(chan struct{})}
	s, a := sendSetup(t, shortLease, snd, 1000)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400) // returns while Send is blocked
	if w.Status != "approved" || len(snd.Calls()) != 0 {
		t.Fatalf("%+v calls %v", w, snd.Calls())
	}
	close(snd.gate)
	payments.WaitSends(s)
	if g := status(t, s, w.ID); g.Status != "paid" || !settled(t, a, w.ID) || a.Balance(u, "usdt") != 600 {
		t.Fatalf("%+v", g)
	}
}

func TestSendFailedRefunds(t *testing.T) {
	snd := &countSender{Sender: &paymentstest.Sender{}}
	snd.SetNext(payments.Result{State: payments.StateFailed, Reason: "closed"})
	s, a := sendSetup(t, shortLease, snd, 1000)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	payments.WaitSends(s)
	if g := status(t, s, w.ID); g.Status != "failed" || g.Reason != "closed" || !settled(t, a, w.ID) {
		t.Fatalf("%+v", g)
	}
	if b := a.Balance(u, "usdt"); b != 1000 {
		t.Fatalf("balance %d", b)
	}
	if o := system(t, a)["outflow"]; o != 0 {
		t.Fatalf("outflow %d", o)
	}
}

func TestSendErrorNeverVoids(t *testing.T) {
	snd := &countSender{Sender: &paymentstest.Sender{}}
	snd.SetErr(errors.New("timeout"))
	s, a := sendSetup(t, shortLease, snd, 1000)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	payments.WaitSends(s)
	held := func(step string) {
		t.Helper()
		if g := status(t, s, w.ID); g.Status != "sending" || settled(t, a, w.ID) {
			t.Fatalf("%s: %+v settled %v", step, g, settled(t, a, w.ID))
		}
		if b, o := a.Balance(u, "usdt"), system(t, a)["outflow"]; b != 600 || o != 0 {
			t.Fatalf("%s: balance %d outflow %d", step, b, o)
		}
	}
	held("send error")
	snd.SetErr(nil)
	snd.SetStatusErr(errors.New("unreachable"))
	for range 3 {
		reconcile(a)
		held("status error")
	}
	snd.SetStatusErr(nil)
	snd.SetStatus(w.ID, payments.Result{State: payments.StatePending, Ref: "tx1"})
	reconcile(a)
	held("pending")
	if g := status(t, s, w.ID); g.Ref != "tx1" {
		t.Fatalf("ref %q", g.Ref)
	}
	snd.SetStatus(w.ID, payments.Result{State: payments.StatePaid})
	reconcile(a)
	if g := status(t, s, w.ID); g.Status != "paid" || g.Ref != "tx1" || !settled(t, a, w.ID) {
		t.Fatalf("%+v", g)
	}
	if b, o := a.Balance(u, "usdt"), system(t, a)["outflow"]; b != 600 || o != 390 {
		t.Fatalf("balance %d outflow %d", b, o)
	}
	if c := snd.Calls(); len(c) != 1 { // only the first Send: Status never said unknown
		t.Fatalf("send calls %v", c)
	}
}

func TestUnknownResends(t *testing.T) {
	snd := &countSender{Sender: &paymentstest.Sender{}}
	snd.SetErr(errors.New("timeout"))
	s, a := sendSetup(t, shortLease, snd, 1000)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	payments.WaitSends(s)
	snd.SetErr(nil) // the sender never saw it: Status says unknown
	reconcile(a)
	if c := snd.Calls(); !slices.Equal(c, []int64{w.ID, w.ID}) {
		t.Fatalf("send calls %v", c)
	}
	if g := status(t, s, w.ID); g.Status != "paid" || len(snd.SentPayouts()) != 1 || a.Balance(u, "usdt") != 600 {
		t.Fatalf("%+v sent %d", g, len(snd.SentPayouts()))
	}
	reconcile(a)
	if len(snd.Calls()) != 2 || len(snd.SentPayouts()) != 1 {
		t.Fatalf("calls %v", snd.Calls())
	}
}

func TestLeaseSingleSender(t *testing.T) {
	ctx := context.Background()
	snd := &countSender{delay: 50 * time.Millisecond}
	s, a := sendSetup(t, payments.Config{Lease: time.Minute, Tick: time.Hour}, snd, 0)
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	race := func(id int64) {
		t.Helper()
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := payments.Advance(ctx, s, id); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
	}
	// Requested rows moved behind the RPC's back, so nothing has sent them yet: one approved, one
	// sending that its Sender never saw (Status: unknown, so it is sent again).
	for i, st := range []string{"approved", "sending"} {
		w := mustWd(t, a, u, st, 400)
		if _, err := a.Pool.Exec(ctx, `UPDATE pay_withdrawals SET status = $2 WHERE id = $1`, w.ID, st); err != nil {
			t.Fatal(err)
		}
		race(w.ID)
		if c := snd.Calls(); len(c) != i+1 || c[i] != w.ID {
			t.Fatalf("%s: send calls %v", st, c)
		}
		if g := status(t, s, w.ID); g.Status != "paid" {
			t.Fatalf("%s: %+v", st, g)
		}
	}
	if b := a.Balance(u, "usdt"); b != 200 {
		t.Fatalf("balance %d", b)
	}
}

func TestRegisterRejectsNegative(t *testing.T) {
	a := apptest.New(t)
	for _, c := range []payments.Config{{Tick: -1}, {Lease: -1}} {
		if _, err := payments.Register(context.Background(), a.App, c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

// deadlineSender blocks each Send until its ctx ends, then checks what the row looks like at that
// moment and pays anyway (the worst case: the payout went out just before the deadline).
type deadlineSender struct {
	*paymentstest.Sender
	check func(ctx context.Context, p payments.Payout)
}

func (d *deadlineSender) Send(ctx context.Context, p payments.Payout) (payments.Result, error) {
	<-ctx.Done()
	d.check(ctx, p)
	return d.Sender.Send(context.Background(), p)
}

// TestSendDeadlineBeforeLease: a Send is cancelled before the row's lease ends (as the DB sees it),
// so while it may still be paying, Resolve is refused and no second worker can claim the row.
func TestSendDeadlineBeforeLease(t *testing.T) {
	snd := &deadlineSender{Sender: &paymentstest.Sender{Method: "wire"}}
	cfg := payments.Config{Lease: time.Second, Tick: time.Hour}
	a := apptest.New(t, apptest.WithCurrencies(
		ledger.CurrencyConfig{Code: "usdt", Ledger: 3, MinAmount: 1, Decimals: 6, Withdrawable: true}))
	s, err := payments.Register(context.Background(), a.App, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sender("usdt", snd, payments.WithdrawLimits{AutoApproveUpTo: 1000}); err != nil {
		t.Fatal(err)
	}
	var checked bool
	snd.check = func(ctx context.Context, p payments.Payout) {
		checked = true
		bg := context.Background()
		dl, ok := ctx.Deadline()
		var lease time.Time
		var live bool
		if err := a.Pool.QueryRow(bg, `SELECT lease_until, lease_until > now() FROM pay_withdrawals WHERE id = $1`, p.ID).Scan(&lease, &live); err != nil {
			t.Error(err)
			return
		}
		if !ok || !dl.Before(lease) || !live {
			t.Errorf("deadline %v (set %v), lease_until %v, live %v", dl, ok, lease, live)
		}
		tx, err := a.Pool.Begin(bg)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(bg)
		if _, err := s.Resolve(bg, tx, p.ID, 9, payments.Result{State: payments.StateFailed, Reason: "gave up"}); !errors.Is(err, payments.ErrSendInProgress) {
			t.Errorf("resolve during send: %v", err)
		}
	}
	u := a.User("ann")
	a.Fund(u, "usdt", 1000)
	w := mustWd(t, a, u, "k", 400)
	payments.WaitSends(s)
	if !checked {
		t.Fatal("send never ran")
	}
	if g := status(t, s, w.ID); g.Status != "paid" || !settled(t, a, w.ID) || a.Balance(u, "usdt") != 600 {
		t.Fatalf("%+v balance %d", g, a.Balance(u, "usdt"))
	}
}
