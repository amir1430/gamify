package payments_test

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentstest"
	"github.com/amir1430/gamify/realtime"
)

// depositSetup registers a usdt receiver "chain" and plays its Run with the test host.
func depositSetup(t *testing.T, l payments.DepositLimits) (*payments.Service, *apptest.App, *paymentstest.Receiver) {
	t.Helper()
	s, a := setup(t)
	r := &paymentstest.Receiver{Method: "chain"}
	if err := s.Receiver("usdt", r, l); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx, payments.FakeHost(s, "chain"))
	for !r.Ready() {
		runtime.Gosched()
	}
	return s, a, r
}

func dep(t *testing.T, a *apptest.App, u int64, amount int64) payments.DepositInfo {
	t.Helper()
	info, cerr := apptest.Call[payments.DepositInfo](a, u, "payments.deposit",
		map[string]any{"currency": "usdt", "method": "chain", "amount": amount})
	if cerr != nil {
		t.Fatal(cerr)
	}
	return info
}

func rows(t *testing.T, a *apptest.App, status string) int {
	t.Helper()
	var n int
	if err := a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pay_deposits WHERE status = $1`, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func updates(a *apptest.App, u int64) int {
	n := 0
	for _, p := range a.Pushes(realtime.UserChannel(u)) {
		var m struct{ Type string }
		if json.Unmarshal(p, &m) == nil && m.Type == "payments.updated" {
			n++
		}
	}
	return n
}

func TestDepositAddressReused(t *testing.T) {
	_, a, r := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	i1, i2 := dep(t, a, u, 0), dep(t, a, u, 0)
	if i1.Address == "" || i1.Address != i2.Address || r.NumCalls() != 1 {
		t.Fatalf("%v %v calls %d", i1, i2, r.NumCalls())
	}
}

func TestDepositUnknownMethod(t *testing.T) {
	_, a, _ := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	_, cerr := apptest.Call[payments.DepositInfo](a, u, "payments.deposit",
		map[string]any{"currency": "usdt", "method": "nope"})
	if cerr == nil || cerr.Code != payments.ErrUnknownMethod.Code {
		t.Fatalf("got %v", cerr)
	}
}

func TestReceivedCreditsOnce(t *testing.T) {
	_, a, r := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	info := dep(t, a, u, 0)
	in := payments.Incoming{ExternalID: "tx1", Address: info.Address, Currency: "usdt", Amount: 700}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Pay(context.Background(), in); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := r.Pay(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if err := r.Pay(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if b := a.Balance(u, "usdt"); b != 700 {
		t.Fatalf("balance %d", b)
	}
	if rows(t, a, "credited") != 1 || updates(a, u) != 1 {
		t.Fatalf("credited %d pushes %d", rows(t, a, "credited"), updates(a, u))
	}
}

func TestReceivedUnknownAddress(t *testing.T) {
	_, a, r := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	if err := r.Pay(context.Background(), payments.Incoming{ExternalID: "x", Address: "nobody", Currency: "usdt", Amount: 5}); err != nil {
		t.Fatal(err)
	}
	if rows(t, a, "unmatched") != 1 || a.Balance(u, "usdt") != 0 {
		t.Fatal("want one unmatched row, no credit")
	}
}

func TestReceivedBelowMin(t *testing.T) {
	_, a, r := depositSetup(t, payments.DepositLimits{Min: 100})
	u := a.User("ann")
	info := dep(t, a, u, 0)
	if err := r.Pay(context.Background(), payments.Incoming{ExternalID: "x", Address: info.Address, Currency: "usdt", Amount: 99}); err != nil {
		t.Fatal(err)
	}
	if rows(t, a, "ignored") != 1 || a.Balance(u, "usdt") != 0 {
		t.Fatal("want ignored, no credit")
	}
}

func TestInvoiceAmountMismatch(t *testing.T) {
	_, a, r := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	info := dep(t, a, u, 500)
	if err := r.Pay(context.Background(), payments.Incoming{ExternalID: "x", Address: info.Address, Currency: "usdt", Amount: 499}); err != nil {
		t.Fatal(err)
	}
	if rows(t, a, "unmatched") != 1 || a.Balance(u, "usdt") != 0 {
		t.Fatal("want unmatched, no credit")
	}
}

func TestCreditingResumes(t *testing.T) {
	s, a, _ := depositSetup(t, payments.DepositLimits{})
	u := a.User("ann")
	if err := payments.LeaveCrediting(context.Background(), s, "chain", "tx9", u, "usdt", 250); err != nil {
		t.Fatal(err)
	}
	if rows(t, a, "crediting") != 1 || a.Balance(u, "usdt") != 0 {
		t.Fatal("setup")
	}
	a.ReconcileNow()
	if rows(t, a, "credited") != 1 || a.Balance(u, "usdt") != 250 || updates(a, u) != 1 {
		t.Fatalf("balance %d", a.Balance(u, "usdt"))
	}
}
