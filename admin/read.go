package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin/internal/store"
	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/auth"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/ledger"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

var errUnknownGame = &apiError{http.StatusBadRequest, "unknown_game", "no such game"}

func badRequest(msg string) *apiError { return &apiError{http.StatusBadRequest, "bad_request", msg} }

// pageParams reads ?limit= (default 50, capped at 100) and ?before= (a positive id, default 0).
func pageParams(r *http.Request) (before int64, limit int32, err error) {
	q := r.URL.Query()
	limit = defaultLimit
	if v := q.Get("limit"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n <= 0 {
			return 0, 0, badRequest("limit must be a positive integer")
		}
		limit = int32(min(n, maxLimit))
	}
	if q.Has("before") {
		before, err = strconv.ParseInt(q.Get("before"), 10, 64)
		if err != nil || before <= 0 {
			return 0, 0, badRequest("before must be a positive integer")
		}
	}
	return before, limit, nil
}

// writePage answers {"items", "next"}: next is the last item's id when the page is full, else null.
func writePage[T any](w http.ResponseWriter, items []T, limit int32, id func(T) int64) {
	if items == nil {
		items = []T{}
	}
	var next *int64
	if len(items) == int(limit) {
		n := id(items[len(items)-1])
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
}

// optInt reads an optional integer filter.
func optInt(r *http.Request, name string) (*int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, badRequest(name + " must be an integer")
	}
	return &n, nil
}

func optStr(r *http.Request, name string) *string {
	if v := r.URL.Query().Get(name); v != "" {
		return &v
	}
	return nil
}

// userID reads {id} of a /api/users/{id} route.
func userID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("user id must be a positive integer")
	}
	return id, nil
}

func (adm *Admin) registerReads() {
	adm.handle("GET /api/dashboard", PermView, adm.dashboard)
	adm.handle("GET /api/users", PermView, adm.users)
	adm.handle("GET /api/users/{id}", PermView, adm.user)
	adm.handle("GET /api/users/{id}/bets", PermView, adm.bets)
	adm.handle("GET /api/users/{id}/seeds", PermView, adm.seeds)
	adm.handle("GET /api/games", PermView, adm.games)
	adm.handle("GET /api/audit", PermView, adm.auditList)
	adm.handle("GET /api/adjustments", PermView, adm.adjustments)
}

type currencyStats struct {
	System      map[string]int64 `json:"system"`
	RewardsPaid int64            `json:"rewards_paid"`
}

func (adm *Admin) dashboard(w http.ResponseWriter, r *http.Request, _ *session) error {
	online, err := adm.a.Realtime.Online(r.Context())
	if err != nil {
		return err
	}
	cur := map[ledger.Currency]currencyStats{}
	for _, c := range adm.a.Ledger.Currencies() {
		sys, err := adm.a.Ledger.SystemBalances(r.Context(), c)
		if err != nil {
			return err
		}
		paid, err := adm.a.Ledger.RewardsPaid(r.Context(), c)
		if err != nil {
			return err
		}
		cur[c] = currencyStats{sys, paid}
	}
	writeJSON(w, http.StatusOK, map[string]any{"online": len(online), "currencies": cur})
	return nil
}

func (adm *Admin) users(w http.ResponseWriter, r *http.Request, _ *session) error {
	before, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	us, err := adm.a.Users.Search(r.Context(), r.URL.Query().Get("q"), before, limit)
	if err != nil {
		return err
	}
	writePage(w, us, limit, func(u auth.User) int64 { return u.ID })
	return nil
}

func (adm *Admin) user(w http.ResponseWriter, r *http.Request, _ *session) error {
	id, err := userID(r)
	if err != nil {
		return err
	}
	us, err := adm.a.Users.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if len(us) == 0 {
		return errNotFound
	}
	ids, err := adm.a.Users.Identities(r.Context(), id)
	if err != nil {
		return err
	}
	bal, err := adm.a.Ledger.Balances(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": us[0], "identities": ids, "balances": bal})
	return nil
}

func (adm *Admin) bets(w http.ResponseWriter, r *http.Request, _ *session) error {
	id, err := userID(r)
	if err != nil {
		return err
	}
	sources := adm.a.BetSources()
	game := r.URL.Query().Get("game")
	if game == "" {
		names := make([]string, 0, len(sources))
		for n := range sources {
			names = append(names, n)
		}
		slices.Sort(names)
		writeJSON(w, http.StatusOK, map[string]any{"games": names})
		return nil
	}
	src, ok := sources[game]
	if !ok {
		return errUnknownGame
	}
	before, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	bets, err := src(r.Context(), id, before, limit)
	if err != nil {
		return err
	}
	writePage(w, bets, limit, func(b app.Bet) int64 { return b.ID })
	return nil
}

func (adm *Admin) seeds(w http.ResponseWriter, r *http.Request, _ *session) error {
	if adm.cfg.Fairness == nil {
		return errNotFound
	}
	id, err := userID(r)
	if err != nil {
		return err
	}
	before, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	ps, err := adm.cfg.Fairness.PairsOf(r.Context(), id, before, limit)
	if err != nil {
		return err
	}
	writePage(w, ps, limit, func(p fairness.Pair) int64 { return p.ID })
	return nil
}

type limitView struct {
	Enabled   bool   `json:"enabled"`
	MinBet    *int64 `json:"min_bet"`
	MaxBet    *int64 `json:"max_bet"`
	MaxPayout *int64 `json:"max_payout"`
}

type gameOverrides struct {
	Enabled *bool                           `json:"enabled"` // nil: no stored setting
	Limits  map[string]map[string]limitView `json:"limits"`  // currency → variant ("" = game) → override
}

// games lists the registered games with their stored overrides and the limits in effect.
func (adm *Admin) games(w http.ResponseWriter, r *http.Request, _ *session) error {
	settings, limits := adm.a.Games.Overrides()
	public := adm.a.Games.Public()
	out := []map[string]any{}
	for _, g := range adm.a.Games.Games() {
		ov := gameOverrides{Limits: map[string]map[string]limitView{}}
		if en, ok := settings[g.Name]; ok {
			ov.Enabled = &en
		}
		for cur, byVariant := range limits[g.Name] {
			ov.Limits[cur] = map[string]limitView{}
			for v, o := range byVariant {
				ov.Limits[cur][v] = limitView{o.Enabled, o.MinBet, o.MaxBet, o.MaxPayout}
			}
		}
		variants := g.Variants
		if variants == nil {
			variants = []string{}
		}
		out = append(out, map[string]any{
			"name": g.Name, "max_bet": g.MaxBet, "max_payout": g.MaxPayout, "variants": variants,
			"pvp": g.PvP, "only": g.Only, "enabled": ov.Enabled == nil || *ov.Enabled,
			"overrides": ov, "effective": public[g.Name],
		})
	}
	slices.SortFunc(out, func(a, b map[string]any) int {
		return strings.Compare(a["name"].(string), b["name"].(string))
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

type auditRow struct {
	ID        int64           `json:"id"`
	AdminID   *int64          `json:"admin_id"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	Reason    *string         `json:"reason"`
	IP        *string         `json:"ip"`
	CreatedAt time.Time       `json:"created_at"`
}

func (adm *Admin) auditList(w http.ResponseWriter, r *http.Request, _ *session) error {
	before, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	who, err := optInt(r, "admin")
	if err != nil {
		return err
	}
	p := store.ListAuditParams{AdminID: who, Action: optStr(r, "action"), Target: optStr(r, "target"), MaxRows: limit}
	if before > 0 {
		p.BeforeID = &before
	}
	rows, err := adm.q.ListAudit(r.Context(), p)
	if err != nil {
		return err
	}
	out := make([]auditRow, len(rows))
	for i, x := range rows {
		out[i] = auditRow{ID: x.ID, AdminID: x.AdminID, Action: x.Action, Target: x.Target, Before: x.Before,
			After: x.After, Reason: x.Reason, CreatedAt: x.CreatedAt.Time}
		if x.Ip != nil {
			s := x.Ip.String()
			out[i].IP = &s
		}
	}
	writePage(w, out, limit, func(a auditRow) int64 { return a.ID })
	return nil
}

// adjustmentStatuses are the values of admin_adjustments.status.
var adjustmentStatuses = []string{"pending_approval", "approved", "done", "failed", "rejected"}

type adjustmentRow struct {
	ID             int64      `json:"id"`
	UserID         int64      `json:"user_id"`
	Currency       string     `json:"currency"`
	Amount         int64      `json:"amount"`
	Reason         string     `json:"reason"`
	RequestedBy    int64      `json:"requested_by"`
	ApprovedBy     *int64     `json:"approved_by"`
	Status         string     `json:"status"`
	Error          *string    `json:"error"`
	IdempotencyKey string     `json:"idempotency_key"`
	CreatedAt      time.Time  `json:"created_at"`
	DoneAt         *time.Time `json:"done_at"`
}

func (adm *Admin) adjustments(w http.ResponseWriter, r *http.Request, _ *session) error {
	before, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	status := optStr(r, "status")
	if status != nil && !slices.Contains(adjustmentStatuses, *status) {
		return badRequest("unknown status")
	}
	user, err := optInt(r, "user")
	if err != nil {
		return err
	}
	p := store.ListAdjustmentsParams{Status: status, UserID: user, MaxRows: limit}
	if before > 0 {
		p.BeforeID = &before
	}
	rows, err := adm.q.ListAdjustments(r.Context(), p)
	if err != nil {
		return err
	}
	out := make([]adjustmentRow, len(rows))
	for i, x := range rows {
		out[i] = adjustmentView(x)
	}
	writePage(w, out, limit, func(a adjustmentRow) int64 { return a.ID })
	return nil
}

func adjustmentView(x store.AdminAdjustment) adjustmentRow {
	v := adjustmentRow{x.ID, x.UserID, x.Currency, x.Amount, x.Reason, x.RequestedBy, x.ApprovedBy,
		x.Status, x.Error, x.IdempotencyKey, x.CreatedAt.Time, nil}
	if x.DoneAt.Valid {
		v.DoneAt = &x.DoneAt.Time
	}
	return v
}
