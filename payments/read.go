package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
	"github.com/amir1430/gamify/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrSameApprover = &realtime.ClientError{Code: 1048, Message: "a second admin must approve"}
	ErrWrongStatus  = errors.New("payments: withdrawal is not in a status that allows this")
	// ErrSendInProgress: a Sender call holds the withdrawal's lease, so it cannot be resolved by hand yet.
	ErrSendInProgress = fmt.Errorf("%w: a send is in progress", ErrWrongStatus)
)

// Filter selects rows for Withdrawals and Deposits; zero fields are no filter. Since is inclusive,
// Until exclusive, both on the creation time.
type Filter struct {
	UserID       int64
	Currency     ledger.Currency
	Method       string
	Status       string
	Since, Until time.Time
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }

// Withdrawals lists withdrawals newest first: before 0 is the first page, else only IDs below it.
func (s *Service) Withdrawals(ctx context.Context, f Filter, before int64, limit int32) ([]Withdrawal, error) {
	rows, err := s.q.ListWithdrawals(ctx, store.ListWithdrawalsParams{BeforeID: before, UserID: f.UserID,
		Currency: string(f.Currency), Method: f.Method, Status: f.Status, Since: ts(f.Since), Until: ts(f.Until), MaxRows: limit})
	out := make([]Withdrawal, len(rows))
	for i, r := range rows {
		out[i] = toWithdrawal(r)
	}
	return out, err
}

// Deposits lists deposits newest first, as Withdrawals does.
func (s *Service) Deposits(ctx context.Context, f Filter, before int64, limit int32) ([]Deposit, error) {
	rows, err := s.q.ListDeposits(ctx, store.ListDepositsParams{BeforeID: before, UserID: f.UserID,
		Currency: string(f.Currency), Method: f.Method, Status: f.Status, Since: ts(f.Since), Until: ts(f.Until), MaxRows: limit})
	out := make([]Deposit, len(rows))
	for i, r := range rows {
		out[i] = toDeposit(r)
	}
	return out, err
}

type historyReq struct {
	Kind   string `json:"kind"` // "deposit", "withdrawal" or empty: both
	Status string `json:"status"`
	Before int64  `json:"before"` // an ID of the kind listed (needs kind): only older rows
	Limit  int32  `json:"limit"`  // 1..100, default 20, per kind
}

type historyResp struct {
	Deposits    []Deposit    `json:"deposits"`
	Withdrawals []Withdrawal `json:"withdrawals"`
}

// historyRPC lists the caller's own rows, newest first; the user is the connection's, never the payload's.
func (s *Service) historyRPC(ctx context.Context, user int64, req historyReq) (historyResp, error) {
	if req.Limit == 0 {
		req.Limit = 20
	}
	// Deposit and withdrawal IDs are separate sequences: one cursor only makes sense for one kind.
	if req.Limit < 1 || req.Limit > 100 || req.Before < 0 || (req.Before > 0 && req.Kind == "") || (req.Kind != "" && req.Kind != "deposit" && req.Kind != "withdrawal") {
		return historyResp{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "bad history request"}
	}
	f := Filter{UserID: user, Status: req.Status}
	out := historyResp{Deposits: []Deposit{}, Withdrawals: []Withdrawal{}}
	var err error
	if req.Kind != "withdrawal" {
		if out.Deposits, err = s.Deposits(ctx, f, req.Before, req.Limit); err != nil {
			return historyResp{}, err
		}
	}
	if req.Kind != "deposit" {
		if out.Withdrawals, err = s.Withdrawals(ctx, f, req.Before, req.Limit); err != nil {
			return historyResp{}, err
		}
	}
	return out, nil
}

// history is the app.HistorySource for deposits and withdrawals merged, newest first. The cursor
// is time: an Activity's ID is its creation time in Unix microseconds, so beforeID is the ID of the
// last row of the previous page. (The row's own ID and kind are in Detail.) Two rows created in the
// same microsecond may straddle a page boundary and one be skipped.
func (s *Service) history(ctx context.Context, userID, beforeID int64, limit int32) ([]app.Activity, error) {
	f := Filter{UserID: userID}
	if beforeID > 0 {
		f.Until = time.UnixMicro(beforeID)
	}
	ds, err := s.Deposits(ctx, f, 0, limit)
	if err != nil {
		return nil, err
	}
	ws, err := s.Withdrawals(ctx, f, 0, limit)
	if err != nil {
		return nil, err
	}
	out := make([]app.Activity, 0, len(ds)+len(ws))
	for _, d := range ds {
		detail, _ := json.Marshal(map[string]any{"kind": "deposit", "id": d.ID, "method": d.Method, "ref": d.ExternalID})
		out = append(out, app.Activity{Game: "payments", ID: d.CreatedAt.UnixMicro(), UserID: d.UserID, Currency: d.Currency,
			Payout: d.Amount, Status: d.Status, Detail: detail, At: d.CreatedAt})
	}
	for _, w := range ws {
		detail, _ := json.Marshal(map[string]any{"kind": "withdrawal", "id": w.ID, "method": w.Method,
			"destination": w.Destination, "ref": w.Ref})
		out = append(out, app.Activity{Game: "payments", ID: w.CreatedAt.UnixMicro(), UserID: w.UserID, Currency: w.Currency,
			Amount: w.Amount, Status: w.Status, Detail: detail, At: w.CreatedAt})
	}
	slices.SortStableFunc(out, func(a, b app.Activity) int { return b.At.Compare(a.At) })
	if int(limit) < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// notDecidable explains why a conditional update matched no row: there is none, it moved, or (a
// sending row) a send holds its lease.
func notDecidable(ctx context.Context, q *store.Queries, id int64, from []string) error {
	r, err := q.GetWithdrawal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if r.Status == "sending" && slices.Contains(from, "sending") {
		return ErrSendInProgress
	}
	return ErrWrongStatus
}

// Approve is an admin approving a `requested` withdrawal, inside the caller's tx; the row is
// locked, so two concurrent calls cannot both be "first". Above the method's SecondApprovalAbove
// the first call only records the admin and leaves it `requested`; the same admin again gets
// ErrSameApprover, another admin makes it `approved`. The send starts with Advance (call it from
// the After hook), else the loop picks it up.
func (s *Service) Approve(ctx context.Context, tx pgx.Tx, id, adminID int64) (Withdrawal, error) {
	q := s.q.WithTx(tx)
	r, err := q.LockWithdrawal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	if r.Status != "requested" {
		return Withdrawal{}, ErrWrongStatus
	}
	_, l, err := s.Limits(ctx, ledger.Currency(r.Currency), r.Method)
	if err != nil { // incl. a method since removed: the second-approval rule is unknown, so never approve
		return Withdrawal{}, err
	}
	if l.SecondApprovalAbove > 0 && r.Amount > l.SecondApprovalAbove {
		switch {
		case r.FirstApprover == adminID:
			return Withdrawal{}, ErrSameApprover
		case r.FirstApprover == 0:
			r, err = q.FirstApprove(ctx, store.FirstApproveParams{ID: id, FirstApprover: adminID})
			if errors.Is(err, pgx.ErrNoRows) {
				return Withdrawal{}, ErrWrongStatus
			}
			return toWithdrawal(r), err
		}
	}
	return s.decide(ctx, q, id, adminID, "approved", []string{"requested"}, false, Result{})
}

func (s *Service) decide(ctx context.Context, q *store.Queries, id, adminID int64, status string, from []string, leaseExpired bool, res Result) (Withdrawal, error) {
	r, err := q.DecideWithdrawal(ctx, store.DecideWithdrawalParams{ID: id, AdminID: adminID, Status: status,
		FromStatuses: from, LeaseExpired: leaseExpired, Ref: res.Ref, Reason: res.Reason})
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, notDecidable(ctx, q, id, from)
	}
	return toWithdrawal(r), err
}

// Reject refuses a `requested` or `approved` withdrawal, inside the caller's tx, never one that is
// `sending` or later (a payout may be in flight; use Resolve). Against the sender's claim of an
// approved row, whichever commits first wins; if the claim does, ErrWrongStatus. The hold is
// released by Advance after the tx commits (call it from the After hook); the reconciler does it
// otherwise.
func (s *Service) Reject(ctx context.Context, tx pgx.Tx, id, adminID int64, reason string) (Withdrawal, error) {
	return s.decide(ctx, s.q.WithTx(tx), id, adminID, "rejected", []string{"requested", "approved"}, false, Result{Reason: reason})
}

// Resolve settles a `sending` withdrawal by hand, inside the caller's tx, when its Sender cannot
// say. It is refused (ErrSendInProgress) while a Send or Status call holds the row's lease, so it
// can never race a payout in flight (a refund of money already sent, or a post of a failed payout);
// retry after the lease. Result: StatePaid with the Ref, or StateFailed with a reason. Advance posts or releases after the
// tx commits.
func (s *Service) Resolve(ctx context.Context, tx pgx.Tx, id, adminID int64, r Result) (Withdrawal, error) {
	var status string
	switch {
	case r.State == StatePaid && r.Ref != "":
		status = "paid"
	case r.State == StateFailed && r.Reason != "":
		status = "failed"
	default:
		return Withdrawal{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "paid needs a ref, failed a reason"}
	}
	return s.decide(ctx, s.q.WithTx(tx), id, adminID, status, []string{"sending"}, true, r)
}

// Advance sends an approved or sending withdrawal, or settles a finished one: for an After hook,
// once the admin's tx has committed. Errors are logged; the loop and reconciler retry.
func (s *Service) Advance(ctx context.Context, id int64) {
	if err := s.advance(ctx, id); err != nil {
		slog.ErrorContext(ctx, "payments: advance withdrawal", "withdrawal", id, "err", err)
	}
}
