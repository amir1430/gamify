package paymentsadmin

import (
	"context"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/payments"
)

// Config: zero fields take the defaults (StuckAfter 1 h, UnmatchedWithin 24 h). StuckAfter is how
// long after its request an approved or sending withdrawal counts as stuck on the dashboard.
// UnmatchedWithin is how far back the dashboard counts deposits no user could be found for.
type Config struct {
	StuckAfter, UnmatchedWithin time.Duration
}

func (c Config) withDefaults() Config {
	if c.StuckAfter <= 0 {
		c.StuckAfter = time.Hour
	}
	if c.UnmatchedWithin <= 0 {
		c.UnmatchedWithin = 24 * time.Hour
	}
	return c
}

// span is d as an operator reads it: "90 min", "2 h", "3 d".
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return itoa(int64(d/(24*time.Hour))) + " d"
	case d >= time.Hour && d%time.Hour == 0:
		return itoa(int64(d/time.Hour)) + " h"
	case d >= time.Minute:
		return itoa(int64(d/time.Minute)) + " min"
	}
	return d.String()
}

func alerts(s *payments.Service, cfg Config) []admin.Alert {
	return []admin.Alert{
		{Name: "pay-approve", Title: "Withdrawals to approve", Hint: "Waiting for an approver",
			Perm: admin.PermPayApprove, View: "pay-withdrawals", Filters: map[string]string{"status": "requested"},
			Count: func(ctx context.Context) (int64, error) {
				return s.CountWithdrawals(ctx, payments.Filter{Status: "requested"})
			}},
		{Name: "pay-stuck", Title: "Withdrawals stuck", Hint: "Approved or sending, requested over " + span(cfg.StuckAfter) + " ago",
			Perm: admin.PermView, Tone: admin.AlertDanger, View: "pay-withdrawals", Filters: map[string]string{"status": "sending"},
			Count: func(ctx context.Context) (int64, error) {
				return s.CountWithdrawals(ctx, payments.Filter{Statuses: []string{"approved", "sending"}, Until: time.Now().Add(-cfg.StuckAfter)})
			}},
		{Name: "pay-unmatched", Title: "Unmatched deposits", Hint: "Money that arrived for no known user, last " + span(cfg.UnmatchedWithin),
			Perm: admin.PermView, View: "pay-deposits", Filters: map[string]string{"status": "unmatched"},
			Count: func(ctx context.Context) (int64, error) {
				return s.CountDeposits(ctx, payments.Filter{Status: "unmatched", Since: time.Now().Add(-cfg.UnmatchedWithin)})
			}},
	}
}

// status is the system page's table of methods: what each serves and how it has done since start.
func status(s *payments.Service) admin.Status {
	at := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t
	}
	return admin.Status{
		Name: "pay-methods", Title: "Payment methods", Perm: admin.PermView,
		Columns: []admin.Column{
			{Key: "method", Label: "Method", Type: admin.Text},
			{Key: "kind", Label: "Kind", Type: admin.Enum, Options: []string{"deposit", "withdraw"}},
			{Key: "currencies", Label: "Currencies", Type: admin.Text},
			{Key: "running", Label: "Running", Type: admin.Bool},
			{Key: "restarts", Label: "Restarts", Type: admin.Int},
			{Key: "last_ok", Label: "Last OK", Type: admin.Time},
			{Key: "last_error", Label: "Last error", Type: admin.Text},
			{Key: "last_error_at", Label: "Error at", Type: admin.Time},
		},
		Rows: func(context.Context) ([]admin.Row, error) {
			rows := []admin.Row{}
			for _, h := range s.Health() {
				cs := make([]string, len(h.Currencies))
				for i, c := range h.Currencies {
					cs[i] = string(c)
				}
				row := admin.Row{"method": h.Name, "kind": h.Kind, "currencies": strings.Join(cs, ", "),
					"last_ok": at(h.LastOK), "last_error": h.LastError, "last_error_at": at(h.LastErrorAt)}
				if h.Kind == "deposit" { // a Sender has no worker of its own
					row["running"], row["restarts"] = h.Running, h.Restarts
				}
				rows = append(rows, row)
			}
			return rows, nil
		},
	}
}
