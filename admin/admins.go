package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amir1430/gamify/admin/internal/store"

	"github.com/jackc/pgx/v5"
)

var (
	errEmailTaken      = &apiError{http.StatusConflict, "email_taken", "a console user with this email exists"}
	errLastOwner       = &apiError{http.StatusConflict, "last_owner", "there must be at least one enabled owner"}
	errSelf            = &apiError{http.StatusConflict, "self", "an owner can't demote or disable themself"}
	errInvalidPassword = &apiError{http.StatusBadRequest, "invalid_password", "the current password is wrong"}
	errWeakPassword    = &apiError{http.StatusBadRequest, "weak_password", "the new password needs at least 12 characters"}
)

const minPasswordLen = 12

func (adm *Admin) registerAdmins() {
	adm.handle("GET /api/admins", PermAdmins, adm.listAdmins)
	adm.handle("POST /api/admins", PermAdmins, adm.createAdmin)
	adm.handle("PATCH /api/admins/{id}", PermAdmins, adm.updateAdmin)
	adm.handle("POST /api/me/password", "", adm.changePassword)
}

// adminView is all an audit row says about a console user: never a hash, password or TOTP data.
type adminView struct {
	Email    string `json:"email"`
	Role     Role   `json:"role"`
	Disabled bool   `json:"disabled"`
}

func viewOf(u store.AdminUser) adminView {
	return adminView{u.Email, Role(u.Role), u.DisabledAt.Valid}
}

func (adm *Admin) listAdmins(w http.ResponseWriter, r *http.Request, _ *session) error {
	rows, err := adm.q.ListAdmins(r.Context())
	if err != nil {
		return err
	}
	type item struct {
		ID           int64     `json:"id"`
		Email        string    `json:"email"`
		Role         string    `json:"role"`
		TelegramID   *int64    `json:"telegram_id"`
		Disabled     bool      `json:"disabled"`
		TOTPEnrolled bool      `json:"totp_enrolled"`
		CreatedAt    time.Time `json:"created_at"`
	}
	items := make([]item, 0, len(rows))
	for _, u := range rows {
		items = append(items, item{u.ID, u.Email, u.Role, u.TelegramID, u.Disabled, u.TotpEnrolled, u.CreatedAt.Time})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (adm *Admin) createAdmin(w http.ResponseWriter, r *http.Request, s *session) error {
	var b struct {
		Email string `json:"email"`
		Role  Role   `json:"role"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	email, err := checkNewUser(b.Email, b.Role)
	if err != nil {
		return badRequest(err.Error())
	}
	password, hash, err := newCredentials()
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		return insertUser(ctx, tx, &s.AdminID, remoteIP(r), email, b.Role, hash)
	})
	if errors.Is(err, ErrEmailTaken) {
		return errEmailTaken
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
	return nil
}

func (adm *Admin) updateAdmin(w http.ResponseWriter, r *http.Request, s *session) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return badRequest("admin id must be a positive integer")
	}
	var b struct {
		Role          *Role `json:"role"`
		Disabled      *bool `json:"disabled"`
		ResetTOTP     bool  `json:"reset_totp"`
		ResetPassword bool  `json:"reset_password"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	if b.Role == nil && b.Disabled == nil && !b.ResetTOTP && !b.ResetPassword {
		return badRequest("nothing to change")
	}
	if b.Role != nil && !b.Role.Valid() {
		return badRequest("unknown role")
	}
	var password, hash string
	if b.ResetPassword {
		if password, hash, err = newCredentials(); err != nil {
			return err
		}
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		// Owners first, then the target (an owner is already locked): one order for everyone.
		owners, err := q.LockEnabledOwners(ctx)
		if err != nil {
			return err
		}
		u, err := q.LockUser(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		before := viewOf(u)
		after := before
		if b.Role != nil {
			after.Role = *b.Role
		}
		if b.Disabled != nil {
			after.Disabled = *b.Disabled
		}
		if before.Role == RoleOwner && !before.Disabled && (after.Role != RoleOwner || after.Disabled) {
			if len(owners) <= 1 {
				return errLastOwner
			}
			if u.ID == s.AdminID {
				return errSelf
			}
		}
		access := after != before
		if !access && !b.ResetTOTP && !b.ResetPassword {
			return nil
		}
		if access {
			if err := q.SetUserAccess(ctx, store.SetUserAccessParams{ID: id, Role: string(after.Role), Disabled: after.Disabled}); err != nil {
				return err
			}
		}
		var did []string
		if b.ResetTOTP {
			if err := q.ResetTOTP(ctx, id); err != nil {
				return err
			}
			did = append(did, "reset_totp")
		}
		if b.ResetPassword {
			if err := q.SetPasswordHash(ctx, store.SetPasswordHashParams{ID: id, PasswordHash: hash}); err != nil {
				return err
			}
			did = append(did, "reset_password")
		}
		if err := q.DeleteSessionsOf(ctx, id); err != nil {
			return err
		}
		return adm.audit(ctx, tx, s, r, "admin.update", adminTarget(id), before, after, strings.Join(did, ","))
	})
	if err != nil {
		return err
	}
	if b.ResetPassword {
		writeJSON(w, http.StatusOK, map[string]string{"password": password})
		return nil
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

func (adm *Admin) changePassword(w http.ResponseWriter, r *http.Request, s *session) error {
	var b struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(w, r, &b); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := adm.q.GetUser(ctx, s.AdminID)
	if err != nil {
		return err
	}
	if !checkPassword(u.PasswordHash, b.Current) {
		return errInvalidPassword
	}
	if utf8.RuneCountInString(b.New) < minPasswordLen {
		return errWeakPassword
	}
	hash, err := hashPassword(b.New)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, adm.a.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.ChangePasswordHash(ctx, store.ChangePasswordHashParams{NewHash: hash, ID: s.AdminID, OldHash: u.PasswordHash})
		if err != nil {
			return err
		}
		if n == 0 { // changed meanwhile: the current password was for the old hash
			return errInvalidPassword
		}
		if err := q.DeleteOtherSessions(ctx, store.DeleteOtherSessionsParams{AdminID: s.AdminID, Keep: s.ID}); err != nil {
			return err
		}
		return adm.audit(ctx, tx, s, r, "admin.password", adminTarget(s.AdminID), nil, nil, "")
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
