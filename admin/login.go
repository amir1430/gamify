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

// maxLoginFails failures for one email (or Telegram ID) within 15 minutes (CountLoginFails),
// counted since the user's last successful login, lock it until the oldest is 15 minutes old.
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
	ctx := r.Context()
	target := "email:" + email
	u, err := adm.q.GetUserByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var uid *int64
	if found {
		uid = &u.ID
	}
	if err := adm.checkLock(r, uid, target); err != nil {
		return err
	}
	if !found {
		checkPassword(dummyHash, req.Password)
		return adm.loginFail(r, nil, target, "unknown", errInvalidLogin)
	}
	if !checkPassword(u.PasswordHash, req.Password) {
		return adm.loginFail(r, uid, target, "password", errInvalidLogin)
	}
	if u.DisabledAt.Valid {
		return adm.loginFail(r, uid, target, "disabled", errInvalidLogin)
	}
	return adm.secondFactor(w, r, u, target, req.TOTP)
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
		// Not counted: the ID in unsigned fields is anyone's to type.
		return adm.loginFail(r, nil, "telegram", "signature", errInvalidLogin)
	}
	ctx := r.Context()
	target := "telegram:" + strconv.FormatInt(tu.ID, 10)
	u, err := adm.q.GetUserByTelegramID(ctx, &tu.ID)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var uid *int64
	if found {
		uid = &u.ID
	}
	if err := adm.checkLock(r, uid, target); err != nil {
		return err
	}
	if !found {
		return adm.loginFail(r, nil, target, "unknown", errInvalidLogin)
	}
	if u.DisabledAt.Valid {
		return adm.loginFail(r, uid, target, "disabled", errInvalidLogin)
	}
	return adm.secondFactor(w, r, u, target, req.TOTP)
}

// checkLock refuses a locked login (before any password check), audited but not counted.
func (adm *Admin) checkLock(r *http.Request, uid *int64, target string) error {
	n, err := adm.q.CountLoginFails(r.Context(), store.CountLoginFailsParams{Target: target, AdminID: uid})
	if err != nil {
		return err
	}
	if n >= maxLoginFails {
		return adm.loginFail(r, uid, target, "locked", errLocked)
	}
	return nil
}

// loginFail audits a failed attempt (reason says why; CountLoginFails says which count) and
// returns answer.
func (adm *Admin) loginFail(r *http.Request, uid *int64, target, reason string, answer *apiError) error {
	var s *session
	if uid != nil {
		s = &session{AdminID: *uid}
	}
	// Written even if the client hangs up, so cancelling can't dodge the count.
	ctx := context.WithoutCancel(r.Context())
	if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		return adm.audit(ctx, tx, s, r, "login.fail", target, nil, nil, reason)
	}); err != nil {
		return err
	}
	return answer
}

// secondFactor finishes a login whose first factor passed: enrolment for a user without TOTP
// (an mfa=false session), otherwise the TOTP code, each step accepted once.
func (adm *Admin) secondFactor(w http.ResponseWriter, r *http.Request, u store.AdminUser, target, code string) error {
	ctx := context.WithoutCancel(r.Context())
	uid := &u.ID
	if u.TotpSecret == nil {
		secret := newTOTPSecret()
		ck, err := adm.startSession(ctx, r, u.ID, false, func(q *store.Queries) error {
			return q.SetTOTPPending(ctx, store.SetTOTPPendingParams{ID: u.ID, TotpPending: adm.totp.seal(u.ID, secret)})
		})
		if err != nil {
			return err
		}
		http.SetCookie(w, ck)
		writeJSON(w, http.StatusOK, map[string]any{"enrol": true,
			"otpauth_uri": otpauthURI(cmp.Or(adm.cfg.Brand.Name, "gamify"), u.Email, secret)})
		return nil
	}
	if code == "" {
		return adm.loginFail(r, uid, target, "totp_required", errTOTPRequired)
	}
	secret, err := adm.totp.open(u.ID, u.TotpSecret)
	if err != nil {
		return err
	}
	step, ok := verifyTOTP(secret, code, time.Now(), u.TotpLastStep)
	if ok {
		// Atomic: of two logins with the same code, one gets the row.
		n, err := adm.q.AcceptTOTPStep(ctx, store.AcceptTOTPStepParams{Step: step, ID: u.ID})
		if err != nil {
			return err
		}
		ok = n == 1
	}
	if !ok {
		return adm.loginFail(r, uid, target, "totp", errInvalidLogin)
	}
	ck, err := adm.startSession(ctx, r, u.ID, true, nil)
	if err != nil {
		return err
	}
	http.SetCookie(w, ck)
	writeJSON(w, http.StatusOK, map[string]any{"enrol": false})
	return nil
}

// startSession creates a session and its login.ok audit row in one transaction (with also, if
// set) and returns the cookie.
func (adm *Admin) startSession(ctx context.Context, r *http.Request, adminID int64, mfa bool, also func(*store.Queries) error) (*http.Cookie, error) {
	var ck *http.Cookie
	err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if also != nil {
			if err := also(q); err != nil {
				return err
			}
		}
		var err error
		if ck, err = newSession(ctx, q, r, adminID, mfa); err != nil {
			return err
		}
		return adm.audit(ctx, tx, &session{AdminID: adminID}, r, "login.ok", adminTarget(adminID), nil, map[string]bool{"mfa": mfa}, "")
	})
	return ck, err
}

// enrol confirms the authenticator app: the code must match the pending secret, which then
// becomes the user's, and this session passes MFA.
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
	if err := pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.ConfirmTOTP(ctx, store.ConfirmTOTPParams{Step: step, ID: u.ID, Pending: u.TotpPending})
		if err != nil {
			return err
		}
		if n != 1 {
			return errNotEnrolling
		}
		if err := q.SetSessionMFA(ctx, s.ID); err != nil {
			return err
		}
		return adm.audit(ctx, tx, s, r, "totp.enrol", adminTarget(u.ID), nil, nil, "")
	}); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}
