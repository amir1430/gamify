package admin

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/ledger"

	"github.com/jackc/pgx/v5"
)

var (
	errIdemConflict  = &apiError{http.StatusConflict, "idempotency_conflict", "the idempotency key was used for a different adjustment"}
	errNotPending    = &apiError{http.StatusConflict, "not_pending", "the adjustment isn't waiting for approval"}
	errOwnAdjustment = &apiError{http.StatusForbidden, "own_adjustment", "you can't decide your own adjustment"}
)

const maxIdemKey = 128

// Adjustment statuses (admin_adjustments.status).
const (
	adjPending  = "pending_approval"
	adjApproved = "approved"
	adjDone     = "done"
	adjFailed   = "failed"
	adjRejected = "rejected"
)

func (adm *Admin) registerAdjustments() {
	adm.handle("POST /api/adjustments", PermAdjust, adm.requestAdjustment)
	adm.handle("POST /api/adjustments/{id}/approve", PermApprove, adm.approveAdjustment)
	adm.handle("POST /api/adjustments/{id}/reject", PermApprove, adm.rejectAdjustment)
}

func adjustTarget(id int64) string { return "adjust:" + strconv.FormatInt(id, 10) }

// adjustAudit is the audit shape of an adjustment.
func adjustAudit(x store.AdminAdjustment) map[string]any {
	return map[string]any{"status": x.Status, "amount": x.Amount, "currency": x.Currency, "user_id": x.UserID}
}

func (adm *Admin) requestAdjustment(w http.ResponseWriter, r *http.Request, s *session) error {
	var b struct {
		UserID         int64  `json:"user_id"`
		Currency       string `json:"currency"`
		Amount         int64  `json:"amount"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	b.Reason = strings.TrimSpace(b.Reason)
	switch {
	case b.Amount == 0 || b.Amount == math.MinInt64:
		return badRequest("amount must be a non-zero integer (minor units)")
	case !adm.a.Ledger.Valid(ledger.Currency(b.Currency)):
		return badRequest("unknown currency")
	case b.IdempotencyKey == "" || len(b.IdempotencyKey) > maxIdemKey:
		return badRequest("idempotency_key is required, at most 128 bytes")
	case b.Reason == "":
		return errReasonRequired
	}
	ctx := r.Context()
	if users, err := adm.a.Users.Get(ctx, b.UserID); err != nil {
		return err
	} else if len(users) == 0 {
		return errNotFound
	}
	status := adjApproved
	if limit, ok := adm.cfg.ApproveAbove[ledger.Currency(b.Currency)]; ok && abs(b.Amount) > limit {
		status = adjPending
	}
	var row store.AdminAdjustment
	inserted := false
	err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		row, err = q.InsertAdjustment(ctx, store.InsertAdjustmentParams{UserID: b.UserID, Currency: b.Currency,
			Amount: b.Amount, Reason: b.Reason, RequestedBy: s.AdminID, Status: status, IdempotencyKey: b.IdempotencyKey})
		if errors.Is(err, pgx.ErrNoRows) { // a replay: the key's row is committed by now
			if row, err = q.GetAdjustmentByKey(ctx, b.IdempotencyKey); err != nil {
				return err
			}
			if row.UserID != b.UserID || row.Currency != b.Currency || row.Amount != b.Amount || row.Reason != b.Reason ||
				row.RequestedBy != s.AdminID {
				return errIdemConflict
			}
			return nil
		}
		if err != nil {
			return err
		}
		inserted = true
		return adm.audit(ctx, tx, s, r, "adjust.request", adjustTarget(row.ID), nil, adjustAudit(row), row.Reason)
	})
	if err != nil {
		return err
	}
	if inserted && row.Status == adjApproved {
		return adm.applyAndAnswer(w, r, row.ID)
	}
	writeJSON(w, http.StatusOK, adjustmentView(row))
	return nil
}

func (adm *Admin) approveAdjustment(w http.ResponseWriter, r *http.Request, s *session) error {
	id, err := adjustmentID(r)
	if err != nil {
		return err
	}
	var b reasonBody
	if r.Body != nil && r.ContentLength != 0 {
		if err := decode(w, r, &b); err != nil {
			return err
		}
	}
	if _, err := adm.decide(r, s, id, adjApproved, "adjust.approve", strings.TrimSpace(b.Reason)); err != nil {
		return err
	}
	return adm.applyAndAnswer(w, r, id)
}

func (adm *Admin) rejectAdjustment(w http.ResponseWriter, r *http.Request, s *session) error {
	id, err := adjustmentID(r)
	if err != nil {
		return err
	}
	reason, err := readReason(w, r)
	if err != nil {
		return err
	}
	row, err := adm.decide(r, s, id, adjRejected, "adjust.reject", reason)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, adjustmentView(row))
	return nil
}

// decide moves a pending adjustment to status (approved or rejected) with its audit row, in one
// transaction holding the row's lock. The requester can't decide their own.
func (adm *Admin) decide(r *http.Request, s *session, id int64, status, action, reason string) (store.AdminAdjustment, error) {
	ctx := r.Context()
	var row store.AdminAdjustment
	err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		before, err := q.LockAdjustment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if before.Status != adjPending {
			return errNotPending
		}
		if before.RequestedBy == s.AdminID {
			return errOwnAdjustment
		}
		if row, err = q.DecideAdjustment(ctx, store.DecideAdjustmentParams{Status: status, ApprovedBy: &s.AdminID, ID: id}); err != nil {
			return err
		}
		return adm.audit(ctx, tx, s, r, action, adjustTarget(id), adjustAudit(before), adjustAudit(row), reason)
	})
	return row, err
}

// applyAndAnswer applies an approved adjustment after its commit and answers the row as it is
// then. An apply that fails for any reason but the ledger refusing it leaves the row approved
// for the reconciler, so it's logged, not an error to the console user.
func (adm *Admin) applyAndAnswer(w http.ResponseWriter, r *http.Request, id int64) error {
	ctx, cancel := sideEffectCtx(r.Context())
	defer cancel()
	if err := adm.apply(ctx, id); err != nil {
		slog.ErrorContext(ctx, "admin: apply adjustment", "id", id, "err", err)
	}
	row, err := adm.q.GetAdjustment(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, adjustmentView(row))
	return nil
}

// apply moves an approved adjustment's money with Ledger.Adjust (its transfer ID is the row's, so
// a repeat moves nothing), after making sure the user's accounts exist, and marks it done, or
// failed when the ledger refuses it. Any other error leaves it approved for the reconciler. Not
// audited: the row's status and done_at record it.
func (adm *Admin) apply(ctx context.Context, id int64) error {
	row, err := adm.q.GetAdjustment(ctx, id)
	if err != nil {
		return err
	}
	return adm.applyRow(ctx, row)
}

func (adm *Admin) applyRow(ctx context.Context, row store.AdminAdjustment) error {
	if row.Status != adjApproved {
		return nil
	}
	// The user's accounts may be missing (their login's EnsureUser failed, or the currency is
	// newer than their last login); login does the same, and it's idempotent.
	if err := adm.a.Ledger.EnsureUser(ctx, row.UserID); err != nil {
		return err
	}
	err := adm.a.Ledger.Adjust(ctx, ledger.Currency(row.Currency), row.ID, row.UserID, row.Amount)
	switch {
	case err == nil:
		n, err := adm.q.FinishAdjustment(ctx, store.FinishAdjustmentParams{Status: adjDone, ID: row.ID})
		if err != nil {
			return err
		}
		if n > 0 {
			if _, err := adm.a.Balances(ctx, row.UserID); err != nil {
				slog.WarnContext(ctx, "admin: push balances", "user", row.UserID, "err", err)
			}
		}
		return nil
	case errors.Is(err, ledger.ErrInsufficientFunds), errors.Is(err, ledger.ErrAlreadyFailed):
		msg := "insufficient funds"
		if !errors.Is(err, ledger.ErrInsufficientFunds) {
			msg = err.Error()
		}
		_, err = adm.q.FinishAdjustment(ctx, store.FinishAdjustmentParams{Status: adjFailed, Error: &msg, ID: row.ID})
		return err
	}
	return err
}

// reconcile applies approved adjustments a crash left behind (older than 10 s, so it doesn't race
// the request applying its own), oldest first, at most 100 a run.
func (adm *Admin) reconcile(ctx context.Context) {
	rows, err := adm.q.DueAdjustments(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "admin: list due adjustments", "err", err)
		return
	}
	for _, row := range rows {
		if err := adm.applyRow(ctx, row); err != nil {
			slog.ErrorContext(ctx, "admin: reconcile adjustment", "id", row.ID, "err", err)
		}
	}
}

// adjustmentID reads {id} of an /api/adjustments/{id} route.
func adjustmentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("adjustment id must be a positive integer")
	}
	return id, nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
