package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
	"github.com/jackc/pgx/v5"
)

func (l DepositLimits) validate() error {
	if l.Min < 0 {
		return errors.New("negative limit")
	}
	return nil
}

func (l WithdrawLimits) validate() error {
	if l.Min < 0 || l.Max < 0 || l.DailyCap < 0 || l.Fee < 0 || l.AutoApproveUpTo < 0 || l.SecondApprovalAbove < 0 {
		return errors.New("negative limit")
	}
	if l.Max != 0 && l.Max < l.Min {
		return fmt.Errorf("max %d below min %d", l.Max, l.Min)
	}
	return nil
}

// Limits returns the method's limits: the Go defaults it was registered with, each side replaced
// whole by the console's row when there is one.
func (s *Service) Limits(ctx context.Context, c ledger.Currency, method string) (DepositLimits, WithdrawLimits, error) {
	k := key{c, method}
	s.mu.Lock()
	d, dok := s.deposit[k]
	w, wok := s.withdraw[k]
	s.mu.Unlock()
	if !dok && !wok {
		return d, w, ErrUnknownMethod
	}
	row, err := s.q.GetLimits(ctx, store.GetLimitsParams{Currency: string(c), Method: method})
	if errors.Is(err, pgx.ErrNoRows) {
		return d, w, nil
	}
	if err != nil {
		return d, w, err
	}
	if row.Deposit != nil {
		if err := json.Unmarshal(row.Deposit, &d); err != nil {
			return d, w, err
		}
	}
	if row.Withdraw != nil {
		if err := json.Unmarshal(row.Withdraw, &w); err != nil {
			return d, w, err
		}
	}
	return d, w, nil
}

// SetLimits saves the console's limits in tx; a nil side is left as it was. The method must be
// registered, and a limit that makes no sense (negative, Max below Min) is refused.
func (s *Service) SetLimits(ctx context.Context, tx pgx.Tx, c ledger.Currency, method string, d *DepositLimits, w *WithdrawLimits) error {
	k := key{c, method}
	s.mu.Lock()
	_, dok := s.deposit[k]
	_, wok := s.withdraw[k]
	s.mu.Unlock()
	if !dok && !wok {
		return ErrUnknownMethod
	}
	q := s.q.WithTx(tx)
	if d != nil {
		if !dok {
			return fmt.Errorf("payments: %s has no deposit method %q", c, method)
		}
		if err := d.validate(); err != nil {
			return err
		}
		b, _ := json.Marshal(d)
		if err := q.SetDepositLimits(ctx, store.SetDepositLimitsParams{Currency: string(c), Method: method, Deposit: b}); err != nil {
			return err
		}
	}
	if w != nil {
		if !wok {
			return fmt.Errorf("payments: %s has no withdrawal method %q", c, method)
		}
		if err := w.validate(); err != nil {
			return err
		}
		b, _ := json.Marshal(w)
		if err := q.SetWithdrawLimits(ctx, store.SetWithdrawLimitsParams{Currency: string(c), Method: method, Withdraw: b}); err != nil {
			return err
		}
	}
	return nil
}
