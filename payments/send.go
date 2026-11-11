package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
	"github.com/jackc/pgx/v5"
)

// advance moves a withdrawal on: an approved one is sent, a sending one whose lease expired is
// asked about (and sent again, with the same Payout.ID, only if its Sender never saw it), a finished
// one is settled. It is the only place that calls Send and Status, under the row's lease (claimed by
// a conditional UPDATE, so one worker at a time) and with no transaction open.
//
// Money safety: an error or a pending answer leaves the row `sending`, held, to be asked again
// after the lease. Only a definite StateFailed voids, only StatePaid posts.
func (s *Service) advance(ctx context.Context, id int64) error {
	r, err := s.q.GetWithdrawal(ctx, id)
	if err != nil {
		return err
	}
	if r.Status != "approved" && r.Status != "sending" {
		return s.settle(ctx, id)
	}
	snd := s.sender(ledger.Currency(r.Currency), r.Method)
	if snd == nil { // a method since removed: the row waits, held, until it is registered again
		return fmt.Errorf("payments: withdrawal %d: no sender %q for %s", id, r.Method, r.Currency)
	}
	from := r.Status
	// The call's deadline is counted from before the claim, less a margin for clock skew, so it ends
	// before the row's lease does (lease_until is the DB's now() + Lease, taken after this point).
	start := time.Now()
	r, err = s.q.ClaimWithdrawal(ctx, store.ClaimWithdrawalParams{ID: id, FromStatus: from, LeaseUs: s.cfg.Lease.Microseconds()})
	if errors.Is(err, pgx.ErrNoRows) { // another worker holds the lease, or the row moved
		return nil
	}
	if err != nil {
		return err
	}
	p := Payout{ID: r.ID, UserID: r.UserID, Currency: ledger.Currency(r.Currency), Amount: r.Amount - r.Fee,
		Destination: r.Destination}
	margin := min(s.cfg.Lease/10, 5*time.Second)
	call, cancel := context.WithDeadline(ctx, start.Add(s.cfg.Lease-margin))
	defer cancel()
	if from == "approved" { // bounded: the call's deadline is already fixed, a slow push only shortens it
		push, cancel := context.WithTimeout(ctx, margin)
		s.pushWithdrawal(push, r)
		cancel()
	}
	var res Result
	if from == "approved" {
		res, err = snd.Send(call, p)
	} else if res, err = snd.Status(call, id); err == nil && res.State == StateUnknown {
		res, err = snd.Send(call, p)
	}
	if err != nil {
		return fmt.Errorf("payments: withdrawal %d: %w", id, err) // stays sending: never voided
	}
	next := "sending" // pending (or anything not definite): asked again after the lease
	switch res.State {
	case StatePaid:
		next = "paid"
	case StateFailed:
		next = "failed"
	}
	m, err := s.q.SendResult(ctx, store.SendResultParams{ID: id, Status: next, Ref: res.Ref, Reason: res.Reason})
	if errors.Is(err, pgx.ErrNoRows) {
		if next != "sending" { // the row left `sending` under a live lease: should not happen
			slog.ErrorContext(ctx, "payments: definite send answer not recorded", "withdrawal", id, "answer", next, "ref", res.Ref)
		}
		return nil
	}
	if err == nil && next == "sending" {
		return nil
	}
	if err != nil {
		return err
	}
	s.pushWithdrawal(ctx, m)
	return s.settle(ctx, id)
}

// sendDue advances the withdrawals waiting to be sent or asked about (the loop and the reconciler).
func (s *Service) sendDue(ctx context.Context) {
	ids, err := s.q.DueWithdrawals(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "payments: list due withdrawals", "err", err)
		return
	}
	for _, id := range ids {
		if err := s.advance(ctx, id); err != nil {
			slog.ErrorContext(ctx, "payments: advance withdrawal", "withdrawal", id, "err", err)
		}
	}
}
