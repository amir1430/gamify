// Package paymentsadmin adds the payments module's pages to the admin console: withdrawals (approve,
// reject, settle by hand), deposits, and the methods with their editable limits. The brand calls
// Register after admin.Register and before App.Run.
package paymentsadmin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments"

	"github.com/jackc/pgx/v5"
)

var (
	withdrawalStatuses = []string{"holding", "requested", "approved", "sending", "paid", "rejected", "cancelled", "failed"}
	depositStatuses    = []string{"crediting", "credited", "ignored", "unmatched"}
)

// Register adds the views pay-withdrawals, pay-deposits and pay-methods.
func Register(adm *admin.Admin, s *payments.Service) error {
	for _, v := range []admin.View{withdrawals(s), deposits(s), methods(s)} {
		if err := adm.View(v); err != nil {
			return err
		}
	}
	return nil
}

func filter(q admin.Query) payments.Filter {
	u, _ := strconv.ParseInt(q.Filters["user_id"], 10, 64) // checked by the console
	return payments.Filter{UserID: u, Currency: ledger.Currency(q.Filters["currency"]), Method: q.Filters["method"], Status: q.Filters["status"]}
}

// refusal turns what an operator can cause into the console's 404 and 409.
func refusal(err error) error {
	switch {
	case errors.Is(err, payments.ErrNotFound):
		return admin.ErrNotFound
	case errors.Is(err, payments.ErrUnknownMethod):
		return admin.Conflict("the payment method is no longer registered")
	case errors.Is(err, payments.ErrSameApprover), errors.Is(err, payments.ErrWrongStatus):
		return admin.Conflict(err.Error())
	}
	return err
}

func withdrawalRow(ctx context.Context, s *payments.Service, w payments.Withdrawal) admin.Row {
	row := admin.Row{"id": w.ID, "user_id": w.UserID, "currency": string(w.Currency), "method": w.Method, "amount": w.Amount,
		"fee": w.Fee, "destination": w.Destination, "status": w.Status, "ref": w.Ref, "reason": w.Reason, "created_at": w.CreatedAt}
	if w.Status == "requested" { // for approve's warning
		if _, l, err := s.Limits(ctx, w.Currency, w.Method); err == nil {
			row["needs_second"] = l.SecondApprovalAbove > 0 && w.Amount > l.SecondApprovalAbove
		}
	}
	return row
}

// total sums a user's rows of one status in a currency, a page at a time.
func total(ctx context.Context, s *payments.Service, user int64, cur ledger.Currency, status string, deposit bool) (sum int64, err error) {
	f := payments.Filter{UserID: user, Currency: cur, Status: status}
	var before int64
	for {
		var ids, amounts []int64
		if deposit {
			var ds []payments.Deposit
			if ds, err = s.Deposits(ctx, f, before, 100); err != nil {
				return 0, err
			}
			for _, d := range ds {
				ids, amounts = append(ids, d.ID), append(amounts, d.Amount)
			}
		} else {
			var ws []payments.Withdrawal
			if ws, err = s.Withdrawals(ctx, f, before, 100); err != nil {
				return 0, err
			}
			for _, w := range ws {
				ids, amounts = append(ids, w.ID), append(amounts, w.Amount)
			}
		}
		for _, a := range amounts {
			sum += a
		}
		if len(ids) < 100 {
			return sum, nil
		}
		before = ids[len(ids)-1]
	}
}

// onlySending is the state of the by-hand settle actions.
func onlySending(r admin.Row) admin.ActionState {
	if r["status"] != "sending" {
		return admin.ActionState{Unavailable: "only a withdrawal that is sending can be settled by hand"}
	}
	return admin.ActionState{}
}

func withdrawals(s *payments.Service) admin.View {
	// act runs one change of a withdrawal and returns the console's before and after.
	act := func(do func(ctx context.Context, tx pgx.Tx, c admin.Call) (payments.Withdrawal, error)) func(context.Context, pgx.Tx, admin.Call) (any, any, error) {
		return func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
			before, err := s.Get(ctx, c.ID)
			if err != nil {
				return nil, nil, refusal(err)
			}
			after, err := do(ctx, tx, c)
			if err != nil {
				return nil, nil, refusal(err)
			}
			return map[string]any{"status": before.Status}, map[string]any{"status": after.Status}, nil
		}
	}
	advance := func(_ context.Context, c admin.Call, _, _ any) { s.AdvanceAsync(c.ID) } // once the tx has committed
	return admin.View{
		Name: "pay-withdrawals", Title: "Withdrawals", Group: "Payments", Perm: admin.PermView, UserFilter: "user_id",
		Summary: []string{"id", "user_id", "amount", "method", "status"},
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Type: admin.UserID},
			{Key: "currency", Type: admin.Text},
			{Key: "method", Type: admin.Text},
			{Key: "amount", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "fee", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "status", Type: admin.Enum, Options: withdrawalStatuses},
			{Key: "destination", Type: admin.Text},
			{Key: "ref", Type: admin.Text},
			{Key: "reason", Type: admin.Text, DetailOnly: true},
			{Key: "deposited", Label: "Credited deposits", Type: admin.Money, CurrencyKey: "currency", DetailOnly: true},
			{Key: "withdrawn", Label: "Paid withdrawals", Type: admin.Money, CurrencyKey: "currency", DetailOnly: true},
			{Key: "created_at", Type: admin.Time},
		},
		Filters: []admin.Filter{
			{Key: "user_id", Type: admin.UserID},
			{Key: "currency", Type: admin.Text},
			{Key: "method", Type: admin.Text},
			{Key: "status", Type: admin.Enum},
		},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			ws, err := s.Withdrawals(ctx, filter(q), q.Before, q.Limit)
			if err != nil {
				return admin.Page{}, err
			}
			p := admin.Page{Items: []admin.Row{}}
			for _, w := range ws {
				p.Items = append(p.Items, withdrawalRow(ctx, s, w))
			}
			if len(ws) == int(q.Limit) {
				p.Next = &ws[len(ws)-1].ID
			}
			return p, nil
		},
		Get: func(ctx context.Context, id int64) (admin.Row, error) {
			w, err := s.Get(ctx, id)
			if err != nil {
				return nil, refusal(err)
			}
			row := withdrawalRow(ctx, s, w)
			if row["deposited"], err = total(ctx, s, w.UserID, w.Currency, "credited", true); err != nil {
				return nil, err
			}
			if row["withdrawn"], err = total(ctx, s, w.UserID, w.Currency, "paid", false); err != nil {
				return nil, err
			}
			return row, nil
		},
		Actions: []admin.Action{
			{Name: "approve", Title: "Approve", Perm: admin.PermPayApprove,
				State: func(r admin.Row) admin.ActionState {
					switch {
					case r["status"] != "requested":
						return admin.ActionState{Unavailable: "only a requested withdrawal can be approved"}
					case r["needs_second"] == true:
						return admin.ActionState{Impact: "needs a second approver: a different admin must approve it too"}
					}
					return admin.ActionState{}
				},
				Run: act(func(ctx context.Context, tx pgx.Tx, c admin.Call) (payments.Withdrawal, error) {
					return s.Approve(ctx, tx, c.ID, c.Admin.ID)
				}), After: advance},
			{Name: "reject", Title: "Reject", Perm: admin.PermPayApprove, Reason: true,
				Confirm: "Reject this withdrawal and give the money back to the player?",
				State: func(r admin.Row) admin.ActionState {
					if r["status"] != "requested" && r["status"] != "approved" {
						return admin.ActionState{Unavailable: "only a requested or approved withdrawal can be rejected"}
					}
					return admin.ActionState{}
				},
				Run: act(func(ctx context.Context, tx pgx.Tx, c admin.Call) (payments.Withdrawal, error) {
					return s.Reject(ctx, tx, c.ID, c.Admin.ID, c.Reason)
				}), After: advance},
			{Name: "mark-paid", Title: "Mark paid", Perm: admin.PermPayResolve, Reason: true, TypeConfirm: "PAID",
				Fields: []admin.Field{{Key: "ref", Label: "Payout reference (tx hash, transfer ID)", Type: admin.Text, Required: true}},
				State: func(r admin.Row) admin.ActionState {
					st := onlySending(r)
					if st.Unavailable == "" {
						st.Impact = "the payout is recorded as sent and the held money is spent"
					}
					return st
				},
				Run: act(func(ctx context.Context, tx pgx.Tx, c admin.Call) (payments.Withdrawal, error) {
					ref, _ := c.Input["ref"].(string)
					if ref = strings.TrimSpace(ref); ref == "" {
						return payments.Withdrawal{}, admin.BadInput("ref is required")
					}
					return s.Resolve(ctx, tx, c.ID, c.Admin.ID, payments.Result{State: payments.StatePaid, Ref: ref, Reason: c.Reason})
				}), After: advance},
			{Name: "mark-failed", Title: "Mark failed", Perm: admin.PermPayResolve, Reason: true, TypeConfirm: "REFUND",
				State: func(r admin.Row) admin.ActionState {
					st := onlySending(r)
					if st.Unavailable == "" {
						st.Impact = "the money goes back to the player; only if the payout surely didn't happen and is not in progress"
					}
					return st
				},
				Run: act(func(ctx context.Context, tx pgx.Tx, c admin.Call) (payments.Withdrawal, error) {
					return s.Resolve(ctx, tx, c.ID, c.Admin.ID, payments.Result{State: payments.StateFailed, Reason: c.Reason})
				}), After: advance},
		},
	}
}

func deposits(s *payments.Service) admin.View {
	return admin.View{
		Name: "pay-deposits", Title: "Deposits", Group: "Payments", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Type: admin.UserID},
			{Key: "currency", Type: admin.Text},
			{Key: "method", Type: admin.Text},
			{Key: "amount", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "status", Type: admin.Enum, Options: depositStatuses},
			{Key: "external_id", Type: admin.Text},
			{Key: "created_at", Type: admin.Time},
		},
		Filters: []admin.Filter{
			{Key: "user_id", Type: admin.UserID},
			{Key: "currency", Type: admin.Text},
			{Key: "method", Type: admin.Text},
			{Key: "status", Type: admin.Enum},
		},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			ds, err := s.Deposits(ctx, filter(q), q.Before, q.Limit)
			if err != nil {
				return admin.Page{}, err
			}
			p := admin.Page{Items: []admin.Row{}}
			for _, d := range ds {
				p.Items = append(p.Items, admin.Row{"id": d.ID, "user_id": d.UserID, "currency": string(d.Currency), "method": d.Method,
					"amount": d.Amount, "status": d.Status, "external_id": d.ExternalID, "created_at": d.CreatedAt})
			}
			if len(ds) == int(q.Limit) {
				p.Next = &ds[len(ds)-1].ID
			}
			return p, nil
		},
	}
}

// minor ends each money field's help: inputs are not converted from decimals.
const minor = "minor units of the method's currency"

// limitFields are the edit inputs; the deposit side only has min and off.
var limitFields = []admin.Field{
	{Key: "min", Label: "Min", Type: admin.Money, Help: "0: no minimum; " + minor},
	{Key: "max", Label: "Max", Type: admin.Money, Help: "withdrawals only; 0: no maximum; " + minor, Group: "Withdrawals only"},
	{Key: "daily_cap", Label: "Daily cap per user (24 h)", Type: admin.Money, Help: "0: no cap; the user's withdrawals in all the currency's methods count against this method's cap; " + minor, Group: "Withdrawals only"},
	{Key: "fee", Label: "Flat fee", Type: admin.Money, Help: minor, Group: "Withdrawals only"},
	{Key: "auto_approve_up_to", Label: "Auto-approve up to", Type: admin.Money, Help: "0: every withdrawal needs an admin; " + minor, Group: "Withdrawals only"},
	{Key: "second_approval_above", Label: "Second approver above", Type: admin.Money, Help: "0: one admin is enough; " + minor, Group: "Withdrawals only"},
	{Key: "off", Label: "Off", Type: admin.Bool, Help: "stops new requests"},
}

func num(c admin.Call, key string) int64 {
	n, _ := c.Input[key].(int64) // absent: 0
	return n
}

func methods(s *payments.Service) admin.View {
	// A row's ID is its place in s.Methods(), which is fixed once the app runs.
	list := func(ctx context.Context) ([]admin.Row, error) {
		rows := []admin.Row{}
		for i, m := range s.Methods() {
			d, w, err := s.Limits(ctx, m.Currency, m.Name)
			if err != nil {
				return nil, err
			}
			row := admin.Row{"id": i + 1, "currency": string(m.Currency), "method": m.Name, "kind": m.Kind}
			if m.Kind == "deposit" {
				row["min"], row["off"] = d.Min, d.Off
			} else {
				row["min"], row["max"], row["daily_cap"], row["fee"], row["auto_approve_up_to"], row["second_approval_above"], row["off"] =
					w.Min, w.Max, w.DailyCap, w.Fee, w.AutoApproveUpTo, w.SecondApprovalAbove, w.Off
			}
			rows = append(rows, row)
		}
		return rows, nil
	}
	money := func(k string) admin.Column {
		return admin.Column{Key: k, Type: admin.Money, CurrencyKey: "currency"}
	}
	return admin.View{
		Name: "pay-methods", Title: "Methods", Group: "Payments", Perm: admin.PermView,
		Summary: []string{"currency", "method", "kind"},
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "currency", Type: admin.Text},
			{Key: "method", Type: admin.Text},
			{Key: "kind", Type: admin.Enum, Options: []string{"deposit", "withdraw"}},
			money("min"), money("max"), money("daily_cap"), money("fee"), money("auto_approve_up_to"), money("second_approval_above"),
			{Key: "off", Type: admin.Bool},
		},
		List: func(ctx context.Context, _ admin.Query) (admin.Page, error) { // every method in one page
			rows, err := list(ctx)
			return admin.Page{Items: rows}, err
		},
		Actions: []admin.Action{{Name: "edit", Title: "Edit limits", Perm: admin.PermPayLimits, Fields: limitFields,
			Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
				ms := s.Methods()
				if c.ID < 1 || c.ID > int64(len(ms)) {
					return nil, nil, admin.ErrNotFound
				}
				m := ms[c.ID-1]
				d, w, err := s.Limits(ctx, m.Currency, m.Name)
				if err != nil {
					return nil, nil, refusal(err)
				}
				off, _ := c.Input["off"].(bool)
				// The audit names the method: a row ID is only its place in today's list.
				audit := func(l any) map[string]any {
					return map[string]any{"currency": m.Currency, "method": m.Name, "kind": m.Kind, "limits": l}
				}
				if m.Kind == "deposit" {
					for _, k := range []string{"max", "daily_cap", "fee", "auto_approve_up_to", "second_approval_above"} {
						if num(c, k) != 0 {
							return nil, nil, admin.BadInput(fmt.Sprintf("%s does not apply to a deposit method", k))
						}
					}
					nd := payments.DepositLimits{Min: num(c, "min"), Off: off}
					return audit(d), audit(nd), limitErr(s.SetLimits(ctx, tx, m.Currency, m.Name, &nd, nil))
				}
				nw := payments.WithdrawLimits{Min: num(c, "min"), Max: num(c, "max"), DailyCap: num(c, "daily_cap"), Fee: num(c, "fee"),
					AutoApproveUpTo: num(c, "auto_approve_up_to"), SecondApprovalAbove: num(c, "second_approval_above"), Off: off}
				return audit(w), audit(nw), limitErr(s.SetLimits(ctx, tx, m.Currency, m.Name, nil, &nw))
			}}},
	}
}

// limitErr: anything SetLimits refuses besides an unknown method is the operator's input.
func limitErr(err error) error {
	if err == nil || errors.Is(err, payments.ErrUnknownMethod) {
		return refusal(err)
	}
	return admin.BadInput(err.Error())
}
