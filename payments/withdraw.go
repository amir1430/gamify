package payments

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
	"github.com/amir1430/gamify/realtime"
	"github.com/jackc/pgx/v5"
)

var (
	ErrBadDestination = &realtime.ClientError{Code: 1042, Message: "bad destination"}
	ErrLimit          = &realtime.ClientError{Code: 1043, Message: "outside the withdrawal limits"}
	ErrDailyCap       = &realtime.ClientError{Code: 1044, Message: "over the daily withdrawal cap"}
	ErrInsufficient   = &realtime.ClientError{Code: 1045, Message: "insufficient withdrawable balance"}
	ErrNotCancellable = &realtime.ClientError{Code: 1046, Message: "withdrawal can no longer be cancelled"}
	ErrWithdrawalsOff = &realtime.ClientError{Code: 1047, Message: "withdrawals are off for this method"}
	ErrNotFound       = &realtime.ClientError{Code: 1049, Message: "withdrawal not found"}
)

// Withdrawal is one withdrawal request, as returned and pushed to the user. Amount is what was
// debited; Amount - Fee is what is sent.
type Withdrawal struct {
	ID          int64           `json:"id"`
	UserID      int64           `json:"user_id"`
	Currency    ledger.Currency `json:"currency"`
	Method      string          `json:"method"`
	Amount      int64           `json:"amount"`
	Fee         int64           `json:"fee"`
	Destination string          `json:"destination"`
	Status      string          `json:"status"`
	Ref         string          `json:"ref"`
	Reason      string          `json:"reason"`
	Settled     bool            `json:"settled"` // the ledger step is done
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func toWithdrawal(r store.PayWithdrawal) Withdrawal {
	return Withdrawal{ID: r.ID, UserID: r.UserID, Currency: ledger.Currency(r.Currency), Method: r.Method,
		Amount: r.Amount, Fee: r.Fee, Destination: r.Destination, Status: r.Status, Ref: r.Ref,
		Reason: r.Reason, Settled: r.Settled, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

type withdrawReq struct {
	Currency    ledger.Currency `json:"currency"`
	Method      string          `json:"method"`
	Amount      int64           `json:"amount"`
	Destination string          `json:"destination"`
	IdemKey     string          `json:"idem_key"`
}

type cancelReq struct {
	ID int64 `json:"id"`
}

func (s *Service) sender(c ledger.Currency, method string) Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.senders[key{c, method}]
}

// Get returns a withdrawal (ErrNotFound if there is none).
func (s *Service) Get(ctx context.Context, id int64) (Withdrawal, error) {
	r, err := s.q.GetWithdrawal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrNotFound
	}
	return toWithdrawal(r), err
}

// withdrawRPC checks the request, stores it as `holding` under the user's lock (the daily cap is
// counted there), and only then takes the ledger hold, keyed by the row ID: a retry or the
// reconciler never holds twice.
func (s *Service) withdrawRPC(ctx context.Context, user int64, req withdrawReq) (Withdrawal, error) {
	if req.IdemKey == "" {
		return Withdrawal{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "idem_key required"}
	}
	byKey := store.WithdrawalByKeyParams{UserID: user, IdemKey: req.IdemKey}
	row, err := s.q.WithdrawalByKey(ctx, byKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, err
	}
	if err != nil { // a new request
		snd := s.sender(req.Currency, req.Method)
		if snd == nil {
			return Withdrawal{}, ErrUnknownMethod
		}
		_, l, err := s.Limits(ctx, req.Currency, req.Method)
		if err != nil {
			return Withdrawal{}, err
		}
		switch {
		case l.Off:
			return Withdrawal{}, ErrWithdrawalsOff
		case req.Amount <= l.Fee || req.Amount < l.Min || (l.Max > 0 && req.Amount > l.Max):
			return Withdrawal{}, ErrLimit
		case snd.ValidateDestination(req.Destination) != nil:
			return Withdrawal{}, ErrBadDestination
		}
		err = pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			if err := q.LockUserWithdrawals(ctx, user); err != nil {
				return err
			}
			if row, err = q.WithdrawalByKey(ctx, byKey); !errors.Is(err, pgx.ErrNoRows) {
				return err // nil: a concurrent retry stored it first
			}
			if l.DailyCap > 0 {
				sum, err := q.WithdrawnSince(ctx, store.WithdrawnSinceParams{UserID: user, Currency: string(req.Currency)})
				if err != nil {
					return err
				}
				if req.Amount > l.DailyCap-sum { // sum + amount could overflow
					return ErrDailyCap
				}
			}
			row, err = q.InsertWithdrawal(ctx, store.InsertWithdrawalParams{UserID: user, Currency: string(req.Currency),
				Method: req.Method, Amount: req.Amount, Fee: l.Fee, Destination: req.Destination, IdemKey: req.IdemKey})
			return err
		})
		if err != nil {
			return Withdrawal{}, err
		}
	}
	holding := row.Status == "holding"
	if row, err = s.hold(ctx, row); err != nil {
		return Withdrawal{}, err
	}
	if holding && row.Status == "failed" {
		return Withdrawal{}, ErrInsufficient
	}
	if row.Status == "approved" { // send now, in the background; the loop and the reconciler retry
		s.goAdvance(context.WithoutCancel(ctx), row.ID)
	}
	return toWithdrawal(row), nil
}

// hold takes the ledger hold for a `holding` row and moves it on: `approved` within the method's
// auto-approve, else `requested`. A hold the ledger refuses is `failed` and settled: nothing was
// held, so it is never voided. Any other row is returned as it is.
func (s *Service) hold(ctx context.Context, r store.PayWithdrawal) (store.PayWithdrawal, error) {
	if r.Status != "holding" {
		return r, nil
	}
	c := ledger.Currency(r.Currency)
	next := store.MoveWithdrawalParams{ID: r.ID, FromStatus: "holding", Status: "requested"}
	err := s.a.Ledger.Withdraw(ctx, Game, c, r.ID, r.UserID, r.Amount)
	switch {
	case errors.Is(err, ledger.ErrInsufficientFunds), errors.Is(err, ledger.ErrAlreadyFailed):
		next.Status, next.Settled, next.Reason = "failed", true, "insufficient funds"
	case errors.Is(err, ledger.ErrNotWithdrawable):
		next.Status, next.Settled, next.Reason = "failed", true, "currency not withdrawable"
	case err != nil:
		return r, err
	default:
		_, l, err := s.Limits(ctx, c, r.Method)
		if err != nil && !errors.Is(err, ErrUnknownMethod) { // a method since removed: a human decides
			return r, err
		}
		if r.Amount <= l.AutoApproveUpTo {
			next.Status = "approved"
		}
	}
	m, err := s.q.MoveWithdrawal(ctx, next)
	if errors.Is(err, pgx.ErrNoRows) { // a concurrent call moved it
		return s.q.GetWithdrawal(ctx, r.ID)
	}
	if err != nil {
		return r, err
	}
	s.pushWithdrawal(ctx, m)
	return m, nil
}

// cancelRPC cancels the caller's own withdrawal while it is `requested`, and releases the hold.
func (s *Service) cancelRPC(ctx context.Context, user int64, req cancelReq) (Withdrawal, error) {
	r, err := s.q.CancelWithdrawal(ctx, store.CancelWithdrawalParams{ID: req.ID, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		r, err = s.q.GetWithdrawal(ctx, req.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows) || (err == nil && r.UserID != user):
			return Withdrawal{}, ErrNotFound
		case err != nil:
			return Withdrawal{}, err
		}
		return Withdrawal{}, ErrNotCancellable
	}
	if err != nil {
		return Withdrawal{}, err
	}
	s.pushWithdrawal(ctx, r)
	if err := s.settle(ctx, r.ID); err != nil {
		slog.WarnContext(ctx, "payments: release cancelled withdrawal", "withdrawal", r.ID, "err", err) // the reconciler retries
	}
	return toWithdrawal(r), nil
}

// settle moves the money of a finished withdrawal once: a paid one is posted (the fee to the
// house), a rejected, cancelled or failed one is released. Rows reach those statuses only after their hold was confirmed (a refused hold is
// failed and already settled), so a void never runs before its hold. Safe to repeat; only the call
// that marks it settled pushes.
func (s *Service) settle(ctx context.Context, id int64) error {
	r, err := s.q.GetWithdrawal(ctx, id)
	if err != nil || r.Settled {
		return err
	}
	c := ledger.Currency(r.Currency)
	switch r.Status {
	case "paid":
		err = s.a.Ledger.PostWithdraw(ctx, Game, c, r.ID, r.UserID, r.Amount, r.Fee)
	case "rejected", "cancelled", "failed":
		err = s.a.Ledger.VoidWithdraw(ctx, Game, c, r.ID, r.UserID, r.Amount)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	r, err = s.q.MarkSettled(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.pushWithdrawal(ctx, r)
	return nil
}

func (s *Service) pushWithdrawal(ctx context.Context, r store.PayWithdrawal) {
	if err := realtime.PublishJSON(ctx, s.a.Realtime, realtime.UserChannel(r.UserID),
		map[string]any{"type": "payments.updated", "withdrawal": toWithdrawal(r)}); err != nil {
		slog.WarnContext(ctx, "push withdrawal", "user", r.UserID, "err", err) // clients re-read
	}
}

// reconcileWithdrawals holds `holding` rows a crash left before their hold, sends or asks about
// due ones, and settles finished ones whose ledger step never ran.
func (s *Service) reconcileWithdrawals(ctx context.Context) {
	holding, err := s.q.HoldingWithdrawals(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "payments: list holding withdrawals", "err", err)
	}
	for _, r := range holding {
		if _, err := s.hold(ctx, r); err != nil {
			slog.ErrorContext(ctx, "payments: hold withdrawal", "withdrawal", r.ID, "err", err)
		}
	}
	s.sendDue(ctx)
	unsettled, err := s.q.UnsettledWithdrawals(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "payments: list unsettled withdrawals", "err", err)
	}
	for _, r := range unsettled {
		if err := s.settle(ctx, r.ID); err != nil {
			slog.ErrorContext(ctx, "payments: settle withdrawal", "withdrawal", r.ID, "err", err)
		}
	}
}
