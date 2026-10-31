package admin

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/amir1430/gamify/ledger"
)

func (adm *Admin) registerTreasury() {
	adm.handle("GET /api/treasury", PermView, adm.treasury)
	adm.handle("GET /api/treasury/{currency}/{account}", PermView, adm.systemHistory)
	adm.handle("GET /api/users/{id}/wallet", PermView, adm.wallet)
}

type systemAccount struct {
	Name           string `json:"name"`
	DebitsPending  int64  `json:"debits_pending"`
	DebitsPosted   int64  `json:"debits_posted"`
	CreditsPending int64  `json:"credits_pending"`
	CreditsPosted  int64  `json:"credits_posted"`
	Net            int64  `json:"net"` // credits_posted - debits_posted
}

type treasuryCurrency struct {
	Accounts []systemAccount `json:"accounts"`
	// What players hold, by double entry: the system accounts' posted nets, negated. Held is what
	// players have on hold (in play): the system accounts' pending credits less pending debits.
	PlayersPosted int64 `json:"players_posted"`
	Held          int64 `json:"held"`
	RewardsPaid   int64 `json:"rewards_paid"`
}

// treasury is every currency's system accounts with their totals, what players hold, and whether
// the ledger driver can read history (kinds: the transfer kinds a history filters on).
func (adm *Admin) treasury(w http.ResponseWriter, r *http.Request, _ *session) error {
	ctx := r.Context()
	l := adm.a.Ledger
	out := map[ledger.Currency]treasuryCurrency{}
	for _, c := range l.Currencies() {
		tc := treasuryCurrency{Accounts: []systemAccount{}}
		for _, name := range ledger.SystemAccounts() {
			t, err := l.Totals(ctx, c, ledger.Party{System: name})
			if err != nil {
				return err
			}
			net := t.CreditsPosted - t.DebitsPosted
			tc.Accounts = append(tc.Accounts, systemAccount{name, t.DebitsPending, t.DebitsPosted, t.CreditsPending, t.CreditsPosted, net})
			tc.PlayersPosted -= net
			tc.Held += t.CreditsPending - t.DebitsPending
		}
		paid, err := l.RewardsPaid(ctx, c)
		if err != nil {
			return err
		}
		tc.RewardsPaid = paid
		out[c] = tc
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": l.HasHistory(), "kinds": ledger.TransferKinds(), "currencies": out})
	return nil
}

func (adm *Admin) systemHistory(w http.ResponseWriter, r *http.Request, _ *session) error {
	c := ledger.Currency(r.PathValue("currency"))
	name := r.PathValue("account")
	if !adm.a.Ledger.Valid(c) || !slices.Contains(ledger.SystemAccounts(), name) {
		return errNotFound
	}
	return adm.history(w, r, c, ledger.Party{System: name})
}

func (adm *Admin) wallet(w http.ResponseWriter, r *http.Request, _ *session) error {
	id, err := userID(r)
	if err != nil {
		return err
	}
	c := ledger.Currency(r.URL.Query().Get("currency"))
	if !adm.a.Ledger.Valid(c) {
		return badRequest("currency must be one of the ledger's")
	}
	return adm.history(w, r, c, ledger.Party{User: id})
}

type historyRow struct {
	ledger.Entry
	ID       string `json:"id"`                  // the transfer ID, 32 hex digits
	GameName string `json:"game_name,omitempty"` // the game's registered name
	Balance  *int64 `json:"balance,omitempty"`   // posted balance after it; unfiltered lists only
}

// history answers one account's entries newest first: ?kind=, ?since=, ?until=, ?limit=, and
// ?before=, the previous page's next. Unfiltered (no kind, no until) each entry carries the
// account's posted balance after it; next then carries the balance to go on from.
func (adm *Admin) history(w http.ResponseWriter, r *http.Request, c ledger.Currency, p ledger.Party) error {
	if !adm.a.Ledger.HasHistory() {
		return &apiError{http.StatusNotImplemented, "no_history", "the ledger driver can't read transfers"}
	}
	q := r.URL.Query()
	_, limit, err := pageParams(withoutBefore(r))
	if err != nil {
		return err
	}
	kind := q.Get("kind")
	if kind != "" && !slices.Contains(ledger.TransferKinds(), kind) {
		return badRequest("unknown kind")
	}
	since, until, err := timeRange(r)
	if err != nil {
		return err
	}
	f := ledger.HistoryFilter{Kind: kind, Limit: uint32(limit)}
	if since.Valid {
		f.Since = since.Time
	}
	if until.Valid {
		f.Until = until.Time
	}
	running := kind == "" && !until.Valid
	var balance int64
	if v := q.Get("before"); v != "" {
		ts, bal, ok := strings.Cut(v, "_")
		n, err := strconv.ParseUint(ts, 10, 64)
		if err != nil || n == 0 {
			return badRequest("before must be a page's next")
		}
		f.Before = n
		if running {
			if !ok {
				running = false
			} else if balance, err = strconv.ParseInt(bal, 10, 64); err != nil {
				return badRequest("before must be a page's next")
			}
		}
	} else if running {
		t, err := adm.a.Ledger.Totals(r.Context(), c, p)
		if err != nil {
			return errNotFound
		}
		balance = t.CreditsPosted - t.DebitsPosted
	}
	es, err := adm.a.Ledger.History(r.Context(), c, p, f)
	if err != nil {
		return err
	}
	out := make([]historyRow, len(es))
	for i, e := range es {
		out[i] = historyRow{Entry: e, ID: fmt.Sprintf("%016x%016x", e.ID.Hi, e.ID.Lo), GameName: adm.a.Ledger.GameName(e.Game)}
		if running {
			b := balance
			out[i].Balance = &b
			balance -= e.Posted
		}
	}
	var next *string
	if len(es) == int(limit) {
		s := strconv.FormatUint(es[len(es)-1].Timestamp, 10)
		if running {
			s += "_" + strconv.FormatInt(balance, 10)
		}
		next = &s
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next": next})
	return nil
}

// withoutBefore is r without ?before= (a history cursor is not an id).
func withoutBefore(r *http.Request) *http.Request {
	q := r.URL.Query()
	q.Del("before")
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q.Encode()
	return r2
}
