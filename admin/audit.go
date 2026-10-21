package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/amir1430/gamify/admin/internal/store"

	"github.com/jackc/pgx/v5"
)

// audit writes one row inside tx, the same transaction as the change it records. s is the acting
// console user (nil: none, e.g. a failed login of an unknown email); target is like "user:42";
// before and after are JSON-encoded (nil = NULL); reason "" = NULL.
func (adm *Admin) audit(ctx context.Context, tx pgx.Tx, s *session, r *http.Request, action, target string, before, after any, reason string) error {
	var id *int64
	if s != nil {
		id = &s.AdminID
	}
	var ip *netip.Addr
	if r != nil {
		ip = remoteIP(r)
	}
	return insertAudit(ctx, store.New(tx), id, ip, action, target, before, after, reason)
}

func insertAudit(ctx context.Context, q *store.Queries, adminID *int64, ip *netip.Addr, action, target string, before, after any, reason string) error {
	b, err := auditJSON(before)
	if err != nil {
		return err
	}
	a, err := auditJSON(after)
	if err != nil {
		return err
	}
	var why *string
	if reason != "" {
		why = &reason
	}
	return q.InsertAudit(ctx, store.InsertAuditParams{AdminID: adminID, Action: action, Target: target,
		Before: b, After: a, Reason: why, Ip: ip})
}

func auditJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
