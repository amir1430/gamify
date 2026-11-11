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

var ErrDepositsOff = &realtime.ClientError{Code: 1041, Message: "deposits are off for this method"}

// Deposit is one incoming payment, as pushed to the user.
type Deposit struct {
	ID         int64           `json:"id"`
	UserID     int64           `json:"user_id"`
	Method     string          `json:"method"`
	Currency   ledger.Currency `json:"currency"`
	Amount     int64           `json:"amount"`
	Status     string          `json:"status"`
	ExternalID string          `json:"external_id"`
	CreatedAt  time.Time       `json:"created_at"`
}

func toDeposit(r store.PayDeposit) Deposit {
	return Deposit{ID: r.ID, UserID: r.UserID, Method: r.Method, Currency: ledger.Currency(r.Currency),
		Amount: r.Amount, Status: r.Status, ExternalID: r.ExternalID, CreatedAt: r.CreatedAt.Time}
}

type depositReq struct {
	Currency ledger.Currency `json:"currency"`
	Method   string          `json:"method"`
	Amount   int64           `json:"amount"` // 0: the user's open address
}

func (s *Service) receiver(c ledger.Currency, method string) Receiver {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receivers[key{c, method}]
}

func (s *Service) depositRPC(ctx context.Context, user int64, req depositReq) (DepositInfo, error) {
	r := s.receiver(req.Currency, req.Method)
	if r == nil {
		return DepositInfo{}, ErrUnknownMethod
	}
	if req.Amount < 0 {
		return DepositInfo{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "bad amount"}
	}
	if req.Amount == 0 {
		t, err := s.q.OpenTarget(ctx, store.OpenTargetParams{Method: req.Method, Currency: string(req.Currency), UserID: user})
		if err == nil {
			return DepositInfo{Address: t.Address}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return DepositInfo{}, err
		}
	}
	info, err := r.Deposit(ctx, DepositRequest{UserID: user, Currency: req.Currency, Amount: req.Amount})
	if err != nil {
		return DepositInfo{}, err
	}
	if info.Address == "" && info.InvoiceID == "" {
		return DepositInfo{}, errors.New("payments: receiver returned neither address nor invoice")
	}
	_, err = s.q.InsertTarget(ctx, store.InsertTargetParams{Method: req.Method, Currency: string(req.Currency),
		UserID: user, Address: info.Address, InvoiceID: info.InvoiceID, Amount: req.Amount})
	if errors.Is(err, pgx.ErrNoRows) && req.Amount == 0 { // a concurrent call stored its address first
		t, err := s.q.OpenTarget(ctx, store.OpenTargetParams{Method: req.Method, Currency: string(req.Currency), UserID: user})
		return DepositInfo{Address: t.Address}, err
	}
	return info, err
}

// Received records an incoming payment and credits the user. Safe to call again for the same
// ExternalID.
func (h *Host) Received(ctx context.Context, in Incoming) error {
	s := h.s
	if in.ExternalID == "" || in.Amount <= 0 {
		return errors.New("payments: Received needs an ExternalID and a positive Amount")
	}
	if s.receiver(in.Currency, h.Method) == nil {
		return ErrUnknownMethod
	}
	row := store.InsertDepositParams{Method: h.Method, Currency: string(in.Currency), ExternalID: in.ExternalID, Amount: in.Amount}
	t, err := s.q.FindTarget(ctx, store.FindTargetParams{Method: h.Method, Address: in.Address, InvoiceID: in.InvoiceID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row.Status, row.Reason = "unmatched", "no such address or invoice"
	case err != nil:
		return err
	default:
		row.UserID, row.TargetID = t.UserID, t.ID
		d, _, err := s.Limits(ctx, in.Currency, h.Method)
		if err != nil {
			return err
		}
		switch {
		case t.Currency != string(in.Currency):
			row.Status, row.Reason = "unmatched", "currency differs from the target's"
		case t.Amount != 0 && t.Amount != in.Amount:
			row.Status, row.Reason = "unmatched", "amount differs from the invoice"
		case in.Amount < d.Min:
			row.Status, row.Reason = "ignored", "below the minimum"
		default:
			row.Status = "crediting"
		}
	}
	dep, err := s.q.InsertDeposit(ctx, row)
	if errors.Is(err, pgx.ErrNoRows) { // seen before: finish it if it never got credited
		dep, err = s.q.GetDeposit(ctx, store.GetDepositParams{Method: h.Method, ExternalID: in.ExternalID})
	}
	if err != nil {
		return err
	}
	if dep.Status != "crediting" {
		return nil
	}
	return s.credit(ctx, dep)
}

// credit moves the money for a committed `crediting` row, then marks it and tells the user. The
// ledger transfer is keyed by the row ID, so running it twice credits once; only the call that
// flips the status pushes.
func (s *Service) credit(ctx context.Context, d store.PayDeposit) error {
	if err := s.a.Ledger.Deposit(ctx, ledger.Currency(d.Currency), d.ID, d.UserID, d.Amount); err != nil {
		return err
	}
	d, err := s.q.MarkCredited(ctx, d.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := realtime.PublishJSON(ctx, s.a.Realtime, realtime.UserChannel(d.UserID),
		map[string]any{"type": "payments.updated", "deposit": toDeposit(d)}); err != nil {
		slog.WarnContext(ctx, "push deposit", "user", d.UserID, "err", err) // clients re-read their balance
	}
	return nil
}

// Reconcile finishes what a crash left half done. Later tasks add their steps here.
func (s *Service) Reconcile(ctx context.Context) {
	rows, err := s.q.CreditingDeposits(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "payments: list crediting deposits", "err", err)
		return
	}
	for _, d := range rows {
		if err := s.credit(ctx, d); err != nil {
			slog.ErrorContext(ctx, "payments: credit deposit", "deposit", d.ID, "err", err)
		}
	}
}
