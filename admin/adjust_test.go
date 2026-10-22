package admin

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/ledger"
)

func adjustBody(user int64, amount int64, key string) map[string]any {
	return map[string]any{"user_id": user, "currency": "usd", "amount": amount, "reason": "goodwill", "idempotency_key": key}
}

func adjustEnv(t *testing.T, approveAbove map[ledger.Currency]int64) (*apptest.App, *Admin, *httptest.Server) {
	t.Helper()
	return newAdmin(t, func(c *Config) { c.ApproveAbove = approveAbove })
}

// /api/me tells the UI which adjustments will wait for approval.
func TestAdjustApproveAboveInMe(t *testing.T) {
	a, _, srv := adjustEnv(t, map[ledger.Currency]int64{"usd": 1000})
	op := loginAs(t, a, srv, "op", RoleOperator)
	st, body := call(t, op, srv, "GET", "/api/me", nil)
	if st != 200 {
		t.Fatalf("me: %d %v", st, body)
	}
	got := map[string]any{}
	for _, c := range body["currencies"].([]any) {
		m := c.(map[string]any)
		got[m["code"].(string)] = m["approve_above"]
	}
	if got["usd"] != 1000.0 || got["tickets"] != nil {
		t.Fatalf("approve_above %v, want usd 1000 and tickets null", got)
	}
}

func TestAdjustCreditDebit(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	op := loginAs(t, a, srv, "op", RoleOperator)
	alice := a.User("alice")

	st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, 500, "k1"))
	if st != 200 || body["status"] != "done" || body["done_at"] == nil || body["amount"] != 500.0 {
		t.Fatalf("credit: %d %v", st, body)
	}
	if b := a.Balance(alice, "usd"); b != 500 {
		t.Fatalf("balance = %d, want 500", b)
	}
	if n := len(a.Pushes(fmt.Sprintf("user:%d", alice))); n != 1 {
		t.Fatalf("balance pushes = %d, want 1", n)
	}
	var after, target, reason string
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT after::text, target, reason FROM admin_audit WHERE action = 'adjust.request'`).Scan(&after, &target, &reason); err != nil {
		t.Fatal(err)
	}
	if target != fmt.Sprintf("adjust:%v", body["id"]) || reason != "goodwill" || !strings.Contains(after, `"status": "approved"`) {
		t.Fatalf("audit: target=%s reason=%s after=%s", target, reason, after)
	}

	st, body = call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, -600, "k2"))
	if st != 200 || body["status"] != "failed" || body["error"] == nil || body["done_at"] == nil {
		t.Fatalf("overdraft: %d %v", st, body)
	}
	if b := a.Balance(alice, "usd"); b != 500 {
		t.Fatalf("balance after refused debit = %d, want 500", b)
	}

	// A replay returns the stored row and writes no audit row; different fields conflict.
	st, again := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, -600, "k2"))
	if st != 200 || again["id"] != body["id"] || again["status"] != "failed" {
		t.Fatalf("replay: %d %v", st, again)
	}
	if n := auditRows(t, a, "adjust.request"); n != 2 {
		t.Fatalf("adjust.request rows = %d, want 2", n)
	}
	st, body = call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, -601, "k2"))
	wantErr(t, "conflict", st, body, 409, "idempotency_conflict")
}

func TestAdjustConcurrentSameKey(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	op := loginAs(t, a, srv, "op", RoleOperator)
	alice := a.User("alice")

	const n = 10
	ids := make([]any, n)
	errs := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, 700, "same"))
			if st != 200 {
				errs[i] = fmt.Sprintf("%d %v", st, body)
			}
			ids[i] = body["id"]
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != "" {
			t.Fatalf("request %d: %s", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("ids differ: %v", ids)
		}
	}
	if b := a.Balance(alice, "usd"); b != 700 {
		t.Fatalf("balance = %d, want 700", b)
	}
	if n := auditRows(t, a, "adjust.request"); n != 1 {
		t.Fatalf("adjust.request rows = %d, want 1", n)
	}
}

func TestAdjustApproval(t *testing.T) {
	a, _, srv := adjustEnv(t, map[ledger.Currency]int64{"usd": 1000})
	op := loginAs(t, a, srv, "op", RoleOperator)
	owner1 := loginAs(t, a, srv, "owner1", RoleOwner)
	owner2 := loginAs(t, a, srv, "owner2", RoleOwner)
	alice := a.User("alice")

	// At the threshold: applied straight away.
	if st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, 1000, "small")); st != 200 || body["status"] != "done" {
		t.Fatalf("small: %d %v", st, body)
	}

	st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, 5000, "big"))
	if st != 200 || body["status"] != "pending_approval" {
		t.Fatalf("big: %d %v", st, body)
	}
	if b := a.Balance(alice, "usd"); b != 1000 {
		t.Fatalf("balance while pending = %d, want 1000", b)
	}
	path := fmt.Sprintf("/api/adjustments/%v", body["id"])

	st, body = call(t, op, srv, "POST", path+"/approve", map[string]any{"reason": "ok"})
	wantErr(t, "operator approves", st, body, 403, "forbidden")

	// owner1's own request (a debit, also above the threshold) can't be approved by owner1.
	st, own := call(t, owner1, srv, "POST", "/api/adjustments", adjustBody(alice, -2000, "own"))
	if st != 200 || own["status"] != "pending_approval" {
		t.Fatalf("own: %d %v", st, own)
	}
	ownPath := fmt.Sprintf("/api/adjustments/%v", own["id"])
	st, body = call(t, owner1, srv, "POST", ownPath+"/approve", map[string]any{"reason": "ok"})
	wantErr(t, "own approve", st, body, 403, "own_adjustment")
	st, body = call(t, owner1, srv, "POST", ownPath+"/reject", map[string]any{"reason": "no"})
	wantErr(t, "own reject", st, body, 403, "own_adjustment")

	st, body = call(t, owner1, srv, "POST", path+"/approve", map[string]any{"reason": "checked"})
	if st != 200 || body["status"] != "done" || body["approved_by"] == nil {
		t.Fatalf("approve: %d %v", st, body)
	}
	if b := a.Balance(alice, "usd"); b != 6000 {
		t.Fatalf("balance = %d, want 6000", b)
	}
	st, body = call(t, owner2, srv, "POST", path+"/approve", map[string]any{"reason": "again"})
	wantErr(t, "approve again", st, body, 409, "not_pending")
	if n := auditRows(t, a, "adjust.approve"); n != 1 {
		t.Fatalf("adjust.approve rows = %d, want 1", n)
	}

	// Reject needs a reason, records who decided and moves no money.
	st, body = call(t, owner2, srv, "POST", ownPath+"/reject", map[string]any{})
	wantErr(t, "reject without reason", st, body, 400, "reason_required")
	st, body = call(t, owner2, srv, "POST", ownPath+"/reject", map[string]any{"reason": "not justified"})
	if st != 200 || body["status"] != "rejected" || body["approved_by"] == nil {
		t.Fatalf("reject: %d %v", st, body)
	}
	if b := a.Balance(alice, "usd"); b != 6000 {
		t.Fatalf("balance after reject = %d, want 6000", b)
	}
	var reason string
	if err := a.Pool.QueryRow(context.Background(), `SELECT reason FROM admin_audit WHERE action = 'adjust.reject'`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "not justified" {
		t.Fatalf("reject audit reason = %q", reason)
	}
	st, body = call(t, owner1, srv, "POST", "/api/adjustments/999999/approve", map[string]any{})
	wantErr(t, "unknown", st, body, 404, "not_found")
}

func TestAdjustReconcile(t *testing.T) {
	a, _, _ := adjustEnv(t, nil)
	addUser(t, a, "op@brand.example", RoleOperator)
	alice := a.User("alice")
	ctx := context.Background()
	// A crash after the commit, before the apply: an approved row nobody applied. A fresh one is
	// left to its request.
	var id int64
	if err := a.Pool.QueryRow(ctx, `INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key, created_at)
		VALUES ($1, 'usd', 300, 'gift', (SELECT id FROM admin_users), 'approved', 'crashed', now() - interval '11 seconds') RETURNING id`, alice).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pool.Exec(ctx, `INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key)
		VALUES ($1, 'usd', 50, 'gift', (SELECT id FROM admin_users), 'approved', 'fresh')`, alice); err != nil {
		t.Fatal(err)
	}
	a.ReconcileNow()
	var status string
	if err := a.Pool.QueryRow(ctx, `SELECT status FROM admin_adjustments WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("status = %s, want done", status)
	}
	if b := a.Balance(alice, "usd"); b != 300 {
		t.Fatalf("balance = %d, want 300", b)
	}
	// Crashed after the ledger call: the row is approved again, the transfer exists.
	if _, err := a.Pool.Exec(ctx, `UPDATE admin_adjustments SET status = 'approved', done_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	a.ReconcileNow()
	if b := a.Balance(alice, "usd"); b != 300 {
		t.Fatalf("balance after second run = %d, want 300", b)
	}
	if err := a.Pool.QueryRow(ctx, `SELECT status FROM admin_adjustments WHERE id = $1`, id).Scan(&status); err != nil || status != "done" {
		t.Fatalf("status after second run = %s %v", status, err)
	}
}

func TestAdjustValidates(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	op := loginAs(t, a, srv, "op", RoleOperator)
	support := loginAs(t, a, srv, "support", RoleSupport)
	alice := a.User("alice")

	with := func(k string, v any) map[string]any {
		b := adjustBody(alice, 100, "key")
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
		return b
	}
	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"zero", with("amount", 0), 400, "bad_request"},
		{"min int64", with("amount", int64(math.MinInt64)), 400, "bad_request"},
		{"currency", with("currency", "eur"), 400, "bad_request"},
		{"user", with("user_id", 999999), 404, "not_found"},
		{"no reason", with("reason", nil), 400, "reason_required"},
		{"blank reason", with("reason", "  "), 400, "reason_required"},
		{"no key", with("idempotency_key", nil), 400, "bad_request"},
		{"long key", with("idempotency_key", strings.Repeat("k", 129)), 400, "bad_request"},
	}
	for _, c := range cases {
		st, body := call(t, op, srv, "POST", "/api/adjustments", c.body)
		wantErr(t, c.name, st, body, c.status, c.code)
	}
	st, body := call(t, support, srv, "POST", "/api/adjustments", adjustBody(alice, 100, "key"))
	wantErr(t, "support", st, body, http.StatusForbidden, "forbidden")
	if b := a.Balance(alice, "usd"); b != 0 {
		t.Fatalf("balance = %d, want 0", b)
	}
	if n := auditRows(t, a, "adjust.request"); n != 0 {
		t.Fatalf("adjust.request rows = %d", n)
	}
}

func TestAdjustUserWithoutAccounts(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	op := loginAs(t, a, srv, "op", RoleOperator)
	// A user row without ledger accounts (EnsureUser failed at login, say).
	u, err := a.Users.Upsert(context.Background(), auth.Identity{Provider: "apptest", Subject: "bob", DisplayName: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(u.ID, 500, "k"))
	if st != 200 || body["status"] != "done" {
		t.Fatalf("credit: %d %v", st, body)
	}
	if b := a.Balance(u.ID, "usd"); b != 500 {
		t.Fatalf("balance = %d, want 500", b)
	}
}

func TestAdjustReplayOtherRequester(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	ownerA := loginAs(t, a, srv, "ownera", RoleOwner)
	ownerB := loginAs(t, a, srv, "ownerb", RoleOwner)
	alice := a.User("alice")
	if st, body := call(t, ownerA, srv, "POST", "/api/adjustments", adjustBody(alice, 500, "k")); st != 200 {
		t.Fatalf("request: %d %v", st, body)
	}
	st, body := call(t, ownerB, srv, "POST", "/api/adjustments", adjustBody(alice, 500, "k"))
	wantErr(t, "other requester", st, body, 409, "idempotency_conflict")
	if b := a.Balance(alice, "usd"); b != 500 {
		t.Fatalf("balance = %d, want 500", b)
	}
}

func TestAdjustReconcileAlreadyFailed(t *testing.T) {
	a, _, srv := adjustEnv(t, nil)
	op := loginAs(t, a, srv, "op", RoleOperator)
	alice := a.User("alice")
	a.Fund(alice, "usd", 100)
	st, body := call(t, op, srv, "POST", "/api/adjustments", adjustBody(alice, -600, "k"))
	if st != 200 || body["status"] != "failed" {
		t.Fatalf("debit: %d %v", st, body)
	}
	ctx := context.Background()
	// A crash between the ledger's refusal and marking the row failed.
	if _, err := a.Pool.Exec(ctx, `UPDATE admin_adjustments SET status = 'approved', error = NULL, done_at = NULL,
		created_at = now() - interval '11 seconds' WHERE id = $1`, int64(body["id"].(float64))); err != nil {
		t.Fatal(err)
	}
	a.Fund(alice, "usd", 1000) // enough now: the refused transfer ID stays refused
	a.ReconcileNow()
	var status string
	var errMsg *string
	if err := a.Pool.QueryRow(ctx, `SELECT status, error FROM admin_adjustments WHERE id = $1`, int64(body["id"].(float64))).Scan(&status, &errMsg); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || errMsg == nil || *errMsg == "" {
		t.Fatalf("status = %s error = %v, want failed with error", status, errMsg)
	}
	if b := a.Balance(alice, "usd"); b != 1100 {
		t.Fatalf("balance = %d, want 1100", b)
	}
}
