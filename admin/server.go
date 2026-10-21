package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/amir1430/gamify/ledger"
)

// apiError is a handler's 4xx answer: {"code", "message"} with Status.
type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

var (
	errUnauthorized = &apiError{http.StatusUnauthorized, "unauthorized", "log in first"}
	errMFARequired  = &apiError{http.StatusUnauthorized, "mfa_required", "set up two-factor authentication first"}
	errForbidden    = &apiError{http.StatusForbidden, "forbidden", "your role can't do this"}
	errCSRF         = &apiError{http.StatusForbidden, "csrf", "missing X-Admin header or wrong Origin"}
	errIPNotAllowed = &apiError{http.StatusForbidden, "ip_not_allowed", "your address may not use the console"}
	errNotFound     = &apiError{http.StatusNotFound, "not_found", "not found"}
	errBadRequest   = &apiError{http.StatusBadRequest, "bad_request", "invalid JSON body"}
)

// access is who may reach a route at all, before its permission.
type access int

const (
	accessMFA     access = iota // a session that passed TOTP (every route but the ones below)
	accessSession               // any session, mfa or not: TOTP enrolment
	accessPublic                // no session: login, logout, the brand's logo
)

// route is what a registered pattern needs. perm "" with accessMFA means any mfa session.
type route struct {
	perm   Perm
	write  bool
	access access
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, s *session) error

// handle registers a route for mfa sessions whose role holds perm ("" = any role).
func (adm *Admin) handle(pattern string, perm Perm, h handlerFunc) {
	adm.add(pattern, route{perm: perm, access: accessMFA}, h)
}

// enrolment registers a route for any session, mfa or not. s is never nil.
func (adm *Admin) enrolment(pattern string, h handlerFunc) {
	adm.add(pattern, route{access: accessSession}, h)
}

// public registers a route without a session. s is nil.
func (adm *Admin) public(pattern string, h handlerFunc) {
	adm.add(pattern, route{access: accessPublic}, h)
}

func (adm *Admin) add(pattern string, rt route, h handlerFunc) {
	method, _, ok := strings.Cut(pattern, " ")
	if !ok {
		panic("admin: route " + pattern + " has no method")
	}
	rt.write = method != http.MethodGet && method != http.MethodHead
	adm.routes[pattern] = rt
	adm.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		var s *session
		if rt.access != accessPublic {
			var err error
			if s, err = adm.session(r); err != nil {
				adm.fail(w, r, err)
				return
			}
			if rt.access == accessMFA && !s.MFA {
				adm.fail(w, r, errMFARequired)
				return
			}
			if rt.perm != "" && !adm.can(s.Role, rt.perm) {
				adm.fail(w, r, errForbidden)
				return
			}
		}
		if err := h(w, r, s); err != nil {
			adm.fail(w, r, err)
		}
	})
}

// guard runs on every request, UI included: AllowIPs (from the connection's address only, never
// X-Forwarded-For), then CSRF on every write (login too): X-Admin: 1 and Origin = Config.Origin.
func (adm *Admin) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(adm.cfg.AllowIPs) > 0 && !adm.allowed(r) {
			adm.fail(w, r, errIPNotAllowed)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			(r.Header.Get("X-Admin") != "1" || r.Header.Get("Origin") != adm.cfg.Origin) {
			adm.fail(w, r, errCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (adm *Admin) allowed(r *http.Request) bool {
	ip := remoteIP(r)
	return ip != nil && slices.ContainsFunc(adm.cfg.AllowIPs, func(p netip.Prefix) bool { return p.Contains(*ip) })
}

// remoteIP is the connection's address, nil if it doesn't parse.
func remoteIP(r *http.Request) *netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	ip := ap.Addr().Unmap()
	return &ip
}

// fail answers an *apiError with its status; anything else is logged (never the request body)
// and answered 500 internal.
func (adm *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *apiError
	if !errors.As(err, &e) {
		slog.ErrorContext(r.Context(), "admin", "method", r.Method, "path", r.URL.Path, "err", err)
		e = &apiError{http.StatusInternalServerError, "internal", "internal error"}
	}
	writeJSON(w, e.Status, map[string]string{"code": e.Code, "message": e.Message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body of at most 64 KiB into v.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}

type meUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

type meBrand struct {
	Name   string  `json:"name"`
	Accent string  `json:"accent"`
	Logo   *string `json:"logo"`
}

type meCurrency struct {
	Code     ledger.Currency `json:"code"`
	Decimals uint8           `json:"decimals"`
}

// me is the UI's start: who's logged in, what they may do, how the console looks, and the
// currencies (ledger order) with their display decimals.
func (adm *Admin) me(w http.ResponseWriter, r *http.Request, s *session) error {
	perms := adm.perms(s.Role)
	b := meBrand{Name: adm.cfg.Brand.Name, Accent: adm.cfg.Brand.Accent}
	if len(adm.cfg.Brand.Logo) > 0 {
		logo := "/api/brand/logo"
		b.Logo = &logo
	}
	cur := []meCurrency{}
	for _, c := range adm.a.Ledger.Currencies() {
		cur = append(cur, meCurrency{c, adm.a.Ledger.Decimals(c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":        meUser{s.AdminID, s.Email, s.Role},
		"permissions": perms,
		"brand":       b,
		"currencies":  cur,
	})
	return nil
}

// brandLogo serves Brand.Logo. An SVG can carry script, so it's served sandboxed.
func (adm *Admin) brandLogo(w http.ResponseWriter, r *http.Request, _ *session) error {
	logo := adm.cfg.Brand.Logo
	if len(logo) == 0 {
		return errNotFound
	}
	h := w.Header()
	h.Set("Content-Type", logoType(logo))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "max-age=300")
	w.Write(logo)
	return nil
}

func logoType(b []byte) string {
	t := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	if bytes.HasPrefix(t, []byte("<svg")) || bytes.HasPrefix(t, []byte("<?xml")) {
		return "image/svg+xml"
	}
	return http.DetectContentType(b)
}
