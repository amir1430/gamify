package paymentsadmin_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/internal/admintest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments"
	"github.com/amir1430/gamify/payments/paymentsadmin"
	"github.com/amir1430/gamify/payments/paymentstest"
)

type env struct {
	a    *apptest.App
	s    *payments.Service
	snd  *paymentstest.Sender
	cons *admintest.Console
	op   *admintest.Client
	u    int64
}

// setup: usdt "wire" (fee 10, manual approval, second approver above 500) and deposit "chain".
func setup(t *testing.T) env {
	t.Helper()
	a, ctx := apptest.New(t, apptest.WithCurrencies(
		ledger.CurrencyConfig{Code: "usdt", Ledger: 3, MinAmount: 1, Decimals: 6, Withdrawable: true})), context.Background()
	s, err := payments.Register(ctx, a.App, payments.Config{Lease: time.Millisecond, Tick: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	snd := &paymentstest.Sender{Method: "wire"}
	if err := s.Sender("usdt", snd, payments.WithdrawLimits{Fee: 10, SecondApprovalAbove: 500}); err != nil {
		t.Fatal(err)
	}
	if err := s.Receiver("usdt", &paymentstest.Receiver{Method: "chain"}, payments.DepositLimits{}); err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admintest.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := paymentsadmin.Register(adm, s); err != nil {
		t.Fatal(err)
	}
	e := env{a: a, s: s, snd: snd, cons: admintest.Start(t, a, adm)}
	e.op = e.cons.Login(admin.RoleOperator)
	e.u = a.User("ann")
	a.Fund(e.u, "usdt", 2000)
	return e
}

func (e env) withdraw(t *testing.T, key string, amount int64) int64 {
	t.Helper()
	w, cerr := apptest.Call[payments.Withdrawal](e.a, e.u, "payments.withdraw", map[string]any{
		"currency": "usdt", "method": "wire", "amount": amount, "destination": "addr", "idem_key": key})
	if cerr != nil {
		t.Fatal(cerr)
	}
	return w.ID
}

func (e env) act(cl *admintest.Client, id int64, name string, body any) (int, map[string]any) {
	return cl.Do("POST", "/api/views/pay-withdrawals/"+strconv.FormatInt(id, 10)+"/actions/"+name, body)
}

func (e env) status(t *testing.T, id int64) string {
	t.Helper()
	w, err := e.s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w.Status
}

func audits(t *testing.T, e env, action string) (n int) {
	t.Helper()
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

func TestApprove(t *testing.T) {
	e := setup(t)
	id := e.withdraw(t, "k", 400)
	if got := e.status(t, id); got != "requested" {
		t.Fatalf("status %s", got)
	}
	if st, _ := e.act(e.cons.Login(admin.RoleSupport), id, "approve", map[string]any{}); st != 403 {
		t.Fatalf("support: %d", st)
	}
	if st, body := e.act(e.op, id, "approve", map[string]any{}); st != 204 {
		t.Fatalf("approve: %d %v", st, body)
	}
	e.s.Wait() // the send runs in the background
	if got := e.status(t, id); got != "paid" {
		t.Fatalf("status %s", got)
	}
	if audits(t, e, "view.pay-withdrawals.approve") != 1 {
		t.Fatal("no audit row")
	}
	if st, _ := e.act(e.op, id, "approve", map[string]any{}); st != 409 { // already decided
		t.Fatalf("again: %d", st)
	}
}

func TestSecondApproval(t *testing.T) {
	e := setup(t)
	id := e.withdraw(t, "k", 600)
	if st, body := e.act(e.op, id, "approve", map[string]any{}); st != 204 {
		t.Fatalf("first: %d %v", st, body)
	}
	st, body := e.act(e.op, id, "approve", map[string]any{})
	if st != 409 || body["error"] == nil && body["message"] == nil {
		t.Fatalf("same admin: %d %v", st, body)
	}
	if got := e.status(t, id); got != "requested" {
		t.Fatalf("status %s", got)
	}
	if st, body := e.act(e.cons.Login(admin.RoleOwner), id, "approve", map[string]any{}); st != 204 {
		t.Fatalf("second: %d %v", st, body)
	}
	e.s.Wait()
	if got := e.status(t, id); got != "paid" {
		t.Fatalf("status %s", got)
	}
}

func TestReject(t *testing.T) {
	e := setup(t)
	id := e.withdraw(t, "k", 400)
	if st, _ := e.act(e.op, id, "reject", map[string]any{}); st != 400 { // reason required
		t.Fatalf("no reason: %d", st)
	}
	if st, body := e.act(e.op, id, "reject", map[string]any{"reason": "kyc"}); st != 204 {
		t.Fatalf("reject: %d %v", st, body)
	}
	e.s.Wait()
	if b := e.a.Balance(e.u, "usdt"); e.status(t, id) != "rejected" || b != 2000 {
		t.Fatalf("balance %d", b)
	}
}

// stuck leaves a sending withdrawal: the sender errors, so it is never settled.
func stuck(t *testing.T, e env) int64 {
	t.Helper()
	e.snd.SetErr(context.DeadlineExceeded)
	id := e.withdraw(t, "k", 400)
	if st, body := e.act(e.op, id, "approve", map[string]any{}); st != 204 {
		t.Fatalf("approve: %d %v", st, body)
	}
	e.s.Wait()
	if got := e.status(t, id); got != "sending" {
		t.Fatalf("status %s", got)
	}
	return id
}

func TestMarkFailedRefunds(t *testing.T) {
	e := setup(t)
	id := e.withdraw(t, "x", 400)
	if st, _ := e.act(e.cons.Login(admin.RoleOwner), id, "mark-failed", map[string]any{"reason": "r"}); st != 409 { // not sending
		t.Fatalf("requested: %d", st)
	}
	e = setup(t)
	id = stuck(t, e)
	time.Sleep(10 * time.Millisecond) // the 1 ms send lease ends
	if st, body := e.act(e.cons.Login(admin.RoleOwner), id, "mark-failed", map[string]any{"reason": "bank said no"}); st != 204 {
		t.Fatalf("mark-failed: %d %v", st, body)
	}
	e.s.Wait()
	if got := e.status(t, id); got != "failed" {
		t.Fatalf("status %s", got)
	}
	if b := e.a.Balance(e.u, "usdt"); b != 2000 {
		t.Fatalf("balance %d, want the refund", b)
	}
}

func TestMarkPaid(t *testing.T) {
	e := setup(t)
	id := stuck(t, e)
	time.Sleep(10 * time.Millisecond)                         // the 1 ms send lease ends
	for _, in := range []map[string]any{nil, {"ref": "  "}} { // ref required
		if st, _ := e.act(e.cons.Login(admin.RoleOwner), id, "mark-paid", map[string]any{"reason": "r", "input": in}); st != 400 {
			t.Fatalf("ref %v: %d", in, st)
		}
	}
	if st, body := e.act(e.cons.Login(admin.RoleOwner), id, "mark-paid", map[string]any{"reason": "wire", "input": map[string]any{"ref": "tx9"}}); st != 204 {
		t.Fatalf("mark-paid: %d %v", st, body)
	}
	e.s.Wait()
	w, _ := e.s.Get(context.Background(), id)
	if w.Status != "paid" || w.Ref != "tx9" || e.a.Balance(e.u, "usdt") != 1600 {
		t.Fatalf("%+v", w)
	}
}

func TestViews(t *testing.T) {
	e := setup(t)
	id := e.withdraw(t, "k", 400)
	uid := strconv.FormatInt(e.u, 10)
	st, body := e.op.Do("GET", "/api/views/pay-withdrawals?user_id="+uid+"&status=requested", nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 1 || items[0].(map[string]any)["amount"] != 400.0 {
		t.Fatalf("list: %d %v", st, body)
	}
	st, body = e.op.Do("GET", "/api/views/pay-withdrawals/"+strconv.FormatInt(id, 10), nil)
	if st != 200 || body["destination"] != "addr" || body["deposited"] != 0.0 || body["withdrawn"] != 0.0 {
		t.Fatalf("detail: %d %v", st, body)
	}
	if st, _ := e.op.Do("GET", "/api/views/pay-withdrawals/999", nil); st != 404 {
		t.Fatalf("missing: %d", st)
	}
	if st, body := e.op.Do("GET", "/api/views/pay-deposits?user_id="+uid, nil); st != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("deposits: %d %v", st, body)
	}
	st, body = e.op.Do("GET", "/api/views/pay-methods", nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 2 {
		t.Fatalf("methods: %d %v", st, body)
	}
}

func TestEditLimits(t *testing.T) {
	e := setup(t)
	_, body := e.op.Do("GET", "/api/views/pay-methods", nil)
	var wid, did any
	for _, it := range body["items"].([]any) {
		r := it.(map[string]any)
		if r["kind"] == "withdraw" {
			wid = r["id"]
		} else {
			did = r["id"]
		}
	}
	path := func(id any) string {
		return "/api/views/pay-methods/" + strconv.Itoa(int(id.(float64))) + "/actions/edit"
	}
	if st, b := e.cons.Login(admin.RoleSupport).Do("POST", path(wid), map[string]any{}); st != 403 {
		t.Fatalf("support: %d %v", st, b)
	}
	owner := e.cons.Login(admin.RoleOwner)
	if st, b := owner.Do("POST", path(wid), map[string]any{"input": map[string]any{"min": 50, "max": 10, "fee": 5}}); st != 400 {
		t.Fatalf("max below min: %d %v", st, b)
	}
	in := map[string]any{"min": 50, "max": 900, "fee": 5, "auto_approve_up_to": 100, "off": true}
	if st, b := owner.Do("POST", path(wid), map[string]any{"input": in}); st != 204 {
		t.Fatalf("edit: %d %v", st, b)
	}
	_, w, err := e.s.Limits(context.Background(), "usdt", "wire")
	if err != nil || w != (payments.WithdrawLimits{Min: 50, Max: 900, Fee: 5, AutoApproveUpTo: 100, Off: true}) {
		t.Fatalf("limits %+v %v", w, err)
	}
	if st, b := owner.Do("POST", path(did), map[string]any{"input": map[string]any{"min": 7}}); st != 204 {
		t.Fatalf("deposit edit: %d %v", st, b)
	}
	if d, _, _ := e.s.Limits(context.Background(), "usdt", "chain"); d.Min != 7 {
		t.Fatalf("deposit %+v", d)
	}
	if st, _ := owner.Do("POST", path(did), map[string]any{"input": map[string]any{"max": 7}}); st != 400 {
		t.Fatalf("withdraw field on a deposit method: %d", st)
	}
	// The audit names the method, not just the row's place in the list.
	var before, after map[string]any
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT before, after FROM admin_audit WHERE action = 'view.pay-methods.edit' ORDER BY id LIMIT 1`).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	for _, m := range []map[string]any{before, after} {
		if m["currency"] != "usdt" || m["method"] != "wire" || m["kind"] != "withdraw" || m["limits"] == nil {
			t.Fatalf("audit before %v after %v", before, after)
		}
	}
	if after["limits"].(map[string]any)["Fee"] != 5.0 {
		t.Fatalf("after %v", after)
	}
}

func TestTreasuryOutflow(t *testing.T) {
	e := setup(t)
	_, body := e.op.Do("GET", "/api/treasury", nil)
	cur, _ := body["currencies"].(map[string]any)
	usdt, _ := cur["usdt"].(map[string]any)
	found := false
	for _, a := range usdt["accounts"].([]any) {
		if a.(map[string]any)["name"] == "outflow" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no outflow in %v", body)
	}
}
