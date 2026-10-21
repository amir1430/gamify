package admin

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/auth/telegram"

	"github.com/jackc/pgx/v5"
)

// maxLoginFails: a target (email or Telegram ID) with this many counted failures in the last 15
// minutes, since its user's last successful login, is refused (429) until the oldest of them
// falls out of the window (CountLoginFails). A sliding window: refused attempts aren't counted.
const maxLoginFails = 5

// dummyHash is checked for an unknown email so the response takes as long as a wrong password and
// doesn't reveal which emails exist. Same parameters as hashPassword.
const dummyHash = "$argon2id$v=19$m=19456,t=2,p=1$ZhL5ZOvjchM96r8hCa6bJQ$1byBCwfv2BUPUAAVVTxDAb/B7tXrdadf1Uv9BkwQYeo"

var (
	errInvalidLogin = &apiError{http.StatusUnauthorized, "invalid_login", "wrong email, password or code"}
	errTOTPRequired = &apiError{http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app"}
	errLocked       = &apiError{http.StatusTooManyRequests, "locked", "too many failed logins, try again in 15 minutes"}
	errInvalidCode  = &apiError{http.StatusBadRequest, "invalid_code", "wrong code"}
	errNotEnrolling = &apiError{http.StatusConflict, "not_enrolling", "no TOTP enrolment in progress, log in again"}
)

// login: email and password, then TOTP (or enrolment on the first login).
func (adm *Admin) login(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := decode(w, r, &req); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if len(email) > 320 {
		return errBadRequest
	}
	return adm.attempt(w, r, "email:"+email, req.TOTP,
		func(ctx context.Context, q *store.Queries) (store.AdminUser, error) {
			return q.GetUserByEmail(ctx, email)
		},
		func(u store.AdminUser, found bool) string {
			switch {
			case !found:
				checkPassword(dummyHash, req.Password)
				return "unknown"
			case !checkPassword(u.PasswordHash, req.Password):
				return "password"
			case u.DisabledAt.Valid:
				return "disabled"
			}
			return ""
		})
}

// loginTelegram: Telegram Login Widget fields instead of the password, then the same TOTP step.
func (adm *Admin) loginTelegram(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Fields map[string]string `json:"fields"`
		TOTP   string            `json:"totp"`
	}
	if err := decode(w, r, &req); err != nil {
		return err
	}
	tu, err := telegram.ValidateLogin(req.Fields, adm.cfg.TelegramBotToken, 24*time.Hour, time.Now())
	if err != nil {
		// Audited, not counted: the ID in unsigned fields is anyone's to type.
		ctx := context.WithoutCancel(r.Context())
		if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
			return adm.audit(ctx, tx, nil, r, "login.fail", "telegram", nil, nil, "signature")
		}); err != nil {
			return err
		}
		return errInvalidLogin
	}
	return adm.attempt(w, r, "telegram:"+strconv.FormatInt(tu.ID, 10), req.TOTP,
		func(ctx context.Context, q *store.Queries) (store.AdminUser, error) {
			return q.GetUserByTelegramID(ctx, &tu.ID)
		},
		func(u store.AdminUser, found bool) string {
			switch {
			case !found:
				return "unknown"
			case u.DisabledAt.Valid:
				return "disabled"
			}
			return ""
		})
}

// outcome is how an attempt ends: a failure to audit and answer, or a session and its response.
type outcome struct {
	reason string    // login.fail's reason; "" = success
	answer *apiError // with reason
	cookie *http.Cookie
	body   any
}

func failed(reason string, answer *apiError) outcome { return outcome{reason: reason, answer: answer} }

// attempt runs one login attempt for target in a single transaction holding the target's advisory
// lock: the lockout count, the first factor (first returns a failure reason or ""), the second
// factor and the audit row, so parallel attempts can't each read a count below the limit. The
// transaction commits failures too: their audit row is the count.
func (adm *Admin) attempt(w http.ResponseWriter, r *http.Request, target, code string,
	lookup func(context.Context, *store.Queries) (store.AdminUser, error),
	first func(u store.AdminUser, found bool) string) error {
	// Not cancelled with the request: hanging up mustn't skip writing a login.fail.
	ctx := context.WithoutCancel(r.Context())
	var out outcome
	err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockLogin(ctx, target); err != nil {
			return err
		}
		u, err := lookup(ctx, q)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var s *session // the audit row's admin: nil for an unknown login
		var uid *int64
		if found {
			s, uid = &session{AdminID: u.ID}, &u.ID
		}
		n, err := q.CountLoginFails(ctx, store.CountLoginFailsParams{Target: target, AdminID: uid})
		if err != nil {
			return err
		}
		if n >= maxLoginFails {
			out = failed("locked", errLocked)
		} else if reason := first(u, found); reason != "" {
			out = failed(reason, errInvalidLogin)
		} else if out, err = adm.secondFactor(ctx, tx, r, u, code); err != nil {
			return err
		}
		if out.reason != "" {
			return adm.audit(ctx, tx, s, r, "login.fail", target, nil, nil, out.reason)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if out.reason != "" {
		return out.answer
	}
	http.SetCookie(w, out.cookie)
	writeJSON(w, http.StatusOK, out.body)
	return nil
}

// secondFactor finishes, inside the attempt's transaction, a login whose first factor passed:
// enrolment for a user without TOTP (an mfa=false session), otherwise the TOTP code, each step
// accepted once.
func (adm *Admin) secondFactor(ctx context.Context, tx pgx.Tx, r *http.Request, u store.AdminUser, code string) (outcome, error) {
	q := store.New(tx)
	if u.TotpSecret == nil {
		secret := newTOTPSecret()
		if err := q.SetTOTPPending(ctx, store.SetTOTPPendingParams{ID: u.ID, TotpPending: adm.totp.seal(u.ID, secret)}); err != nil {
			return outcome{}, err
		}
		ck, err := adm.startSession(ctx, tx, r, u.ID, false)
		return outcome{cookie: ck, body: map[string]any{"enrol": true,
			"otpauth_uri": otpauthURI(cmp.Or(adm.cfg.Brand.Name, "gamify"), u.Email, secret)}}, err
	}
	if code == "" {
		return failed("totp_required", errTOTPRequired), nil
	}
	secret, err := adm.totp.open(u.ID, u.TotpSecret)
	if err != nil {
		return outcome{}, err
	}
	step, ok := verifyTOTP(secret, code, time.Now(), u.TotpLastStep)
	if ok {
		// Atomic: of two logins with the same code, one gets the row (it stays locked until
		// this transaction, with its login.ok row, commits).
		n, err := q.AcceptTOTPStep(ctx, store.AcceptTOTPStepParams{Step: step, ID: u.ID})
		if err != nil {
			return outcome{}, err
		}
		ok = n == 1
	}
	if !ok {
		return failed("totp", errInvalidLogin), nil
	}
	ck, err := adm.startSession(ctx, tx, r, u.ID, true)
	return outcome{cookie: ck, body: map[string]any{"enrol": false}}, err
}

// startSession creates a session and its login.ok audit row in tx and returns the cookie to set
// once tx commits.
func (adm *Admin) startSession(ctx context.Context, tx pgx.Tx, r *http.Request, adminID int64, mfa bool) (*http.Cookie, error) {
	ck, err := newSession(ctx, store.New(tx), r, adminID, mfa)
	if err != nil {
		return nil, err
	}
	return ck, adm.audit(ctx, tx, &session{AdminID: adminID}, r, "login.ok", adminTarget(adminID), nil, map[string]bool{"mfa": mfa}, "")
}

// enrol confirms the authenticator app: the code must match the pending secret, which then
// becomes the user's. The mfa=false session is replaced by a new mfa one (a new token), so a
// token leaked before enrolment never becomes a full session.
func (adm *Admin) enrol(w http.ResponseWriter, r *http.Request, s *session) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := adm.q.GetUser(ctx, s.AdminID)
	if err != nil {
		return err
	}
	if u.TotpPending == nil {
		return errNotEnrolling
	}
	secret, err := adm.totp.open(u.ID, u.TotpPending)
	if err != nil {
		return err
	}
	step, ok := verifyTOTP(secret, req.Code, time.Now(), u.TotpLastStep)
	if !ok {
		return errInvalidCode
	}
	var ck *http.Cookie
	if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.ConfirmTOTP(ctx, store.ConfirmTOTPParams{Step: step, ID: u.ID, Pending: u.TotpPending})
		if err != nil {
			return err
		}
		if n != 1 {
			return errNotEnrolling
		}
		if err := q.DeleteSession(ctx, s.ID); err != nil {
			return err
		}
		if ck, err = newSession(ctx, q, r, u.ID, true); err != nil {
			return err
		}
		return adm.audit(ctx, tx, s, r, "totp.enrol", adminTarget(u.ID), nil, nil, "")
	}); err != nil {
		return err
	}
	http.SetCookie(w, ck)
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}
