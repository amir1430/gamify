package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"github.com/amir1430/gamify/admin/internal/store"

	"github.com/jackc/pgx/v5"
)

// cookieName holds the session token: 32 random bytes, base64url. The table keeps its SHA-256.
// Timeouts (idle 30 min, absolute 12 h) and the once-a-minute seen_at refresh are in GetSession.
const cookieName = "gamify_admin"

// session is the console user behind a request.
type session struct {
	ID, AdminID int64
	Email       string
	Role        Role
	MFA         bool
}

func adminTarget(id int64) string { return "admin:" + strconv.FormatInt(id, 10) }

// newSession stores a session inside q's transaction and returns the cookie to set once it commits.
func newSession(ctx context.Context, q *store.Queries, r *http.Request, adminID int64, mfa bool) (*http.Cookie, error) {
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(tok)
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{AdminID: adminID, Hash: sum[:], Mfa: mfa,
		Ip: remoteIP(r), UserAgent: &ua}); err != nil {
		return nil, err
	}
	return &http.Cookie{Name: cookieName, Value: base64.RawURLEncoding.EncodeToString(tok), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}, nil
}

// expiredCookie removes the session cookie from the browser.
func expiredCookie() *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

// cookieHash is the stored hash of the request's session cookie.
func cookieHash(r *http.Request) ([]byte, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, false
	}
	tok, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(tok) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(tok)
	return sum[:], true
}

// session is the request's live session, or errUnauthorized. A timed-out session, or one of a
// disabled user, is deleted.
func (adm *Admin) session(r *http.Request) (*session, error) {
	ctx := r.Context()
	h, ok := cookieHash(r)
	if !ok {
		return nil, errUnauthorized
	}
	row, err := adm.q.GetSession(ctx, h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if row.Expired || row.Disabled {
		if err := adm.q.DeleteSession(ctx, row.ID); err != nil {
			return nil, err
		}
		return nil, errUnauthorized
	}
	if row.Stale {
		if err := adm.q.TouchSession(ctx, row.ID); err != nil {
			return nil, err
		}
	}
	return &session{ID: row.ID, AdminID: row.AdminID, Email: row.Email, Role: Role(row.Role), MFA: row.Mfa}, nil
}

// logout deletes the request's session, if any, and expires the cookie.
func (adm *Admin) logout(w http.ResponseWriter, r *http.Request, _ *session) error {
	ctx := r.Context()
	if h, ok := cookieHash(r); ok {
		row, err := adm.q.GetSession(ctx, h)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			s := &session{ID: row.ID, AdminID: row.AdminID, Email: row.Email, Role: Role(row.Role), MFA: row.Mfa}
			if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
				if err := store.New(tx).DeleteSession(ctx, s.ID); err != nil {
					return err
				}
				return adm.audit(ctx, tx, s, r, "logout", adminTarget(s.AdminID), nil, nil, "")
			}); err != nil {
				return err
			}
		}
	}
	http.SetCookie(w, expiredCookie())
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}
