package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrNoHistory: the driver can't read transfers (it has no TransferReader).
var ErrNoHistory = errors.New("ledger: driver can't read transfers")

// Party is one side of a transfer: a user's account (User) or a system account (System, a
// SystemAccounts name), in the currency at hand.
type Party struct {
	User   int64  `json:"user,omitempty"`
	System string `json:"system,omitempty"`
}

// SystemAccounts names every system account of a currency, in a stable order.
func SystemAccounts() []string {
	kinds := make([]uint64, 0, len(systemKinds))
	for k := range systemKinds {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = systemKinds[k]
	}
	return out
}

// Transfer kinds, as Entry.Kind and HistoryFilter.Kind name them.
var transferKinds = map[uint16]string{codeCharge: "charge", codePayout: "payout", codeTopUp: "top_up",
	codeDeposit: "deposit", codeWelcome: "welcome", codeSwap: "swap", codeReward: "reward",
	codeAdjustment: "adjustment", codeSpend: "spend", codeTransfer: "transfer", codeFloor: "floor"}

// TransferKinds names every kind of transfer, in a stable order.
func TransferKinds() []string {
	codes := make([]uint16, 0, len(transferKinds))
	for c := range transferKinds {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = transferKinds[c]
	}
	return out
}

func kindName(code uint16) string {
	if k, ok := transferKinds[code]; ok {
		return k
	}
	return fmt.Sprintf("code_%d", code)
}

// Entry is one transfer on an account, seen from that account.
type Entry struct {
	ID        ID        `json:"-"`
	Timestamp uint64    `json:"-"` // the driver's; HistoryFilter.Before pages by it
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`           // TransferKinds, or "code_<n>"
	Game      Game      `json:"game,omitempty"` // the game whose operation moved it; 0: none
	Op        int64     `json:"op,string"`      // the operation, deposit, claim or adjustment ID
	// Stage: "posted" (moved for good), "held" (a pending hold), "settled" (posts a hold),
	// "released" (voids a hold).
	Stage  string `json:"stage"`
	Amount int64  `json:"amount"` // > 0 credits this account, < 0 debits it
	// Posted is the change to credits_posted - debits_posted: Amount, except 0 for holds and their
	// release. A running posted balance sums it.
	Posted int64 `json:"posted"`
	Other  Party `json:"other"`
}

// HistoryFilter selects entries, newest first.
type HistoryFilter struct {
	Before uint64    // Timestamp of the last entry of the previous page; 0: from the newest
	Since  time.Time // zero: no bound
	Until  time.Time // exclusive; zero: no bound
	Kind   string    // one of TransferKinds; "": any
	Limit  uint32
}

// GameName is the name a game was registered with (RegisterGame); "" if none.
func (l *Ledger) GameName(g Game) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.games[g]
}

// HasHistory reports whether History can read transfers.
func (l *Ledger) HasHistory() bool { _, ok := l.d.(TransferReader); return ok }

// account resolves a party in c to its account ID.
func (l *Ledger) account(c Currency, p Party) (CurrencyConfig, ID, error) {
	cur, err := l.currency(c)
	if err != nil {
		return cur, ID{}, err
	}
	if p.System == "" {
		if p.User <= 0 {
			return cur, ID{}, errors.New("ledger: no account named")
		}
		return cur, userAccountID(cur, p.User), nil
	}
	for k, name := range systemKinds {
		if name == p.System {
			return cur, system(cur, k), nil
		}
	}
	return cur, ID{}, fmt.Errorf("ledger: unknown system account %q", p.System)
}

// Totals reads an account's running totals (a user's or a system account).
func (l *Ledger) Totals(ctx context.Context, c Currency, p Party) (AccountBalance, error) {
	_, id, err := l.account(c, p)
	if err != nil {
		return AccountBalance{}, err
	}
	accs, err := l.d.LookupAccounts(ctx, []ID{id})
	if err != nil {
		return AccountBalance{}, err
	}
	if len(accs) == 0 {
		return AccountBalance{}, fmt.Errorf("ledger: no %v account in %q", p, c)
	}
	return accs[0], nil
}

// History reads an account's entries newest first (ErrNoHistory without a TransferReader).
func (l *Ledger) History(ctx context.Context, c Currency, p Party, f HistoryFilter) ([]Entry, error) {
	r, ok := l.d.(TransferReader)
	if !ok {
		return nil, ErrNoHistory
	}
	_, id, err := l.account(c, p)
	if err != nil {
		return nil, err
	}
	af := AccountFilter{AccountID: id, Limit: f.Limit, Debits: true, Credits: true, Reversed: true}
	if f.Kind != "" {
		for code, k := range transferKinds {
			if k == f.Kind {
				af.Code = code
			}
		}
		if af.Code == 0 {
			return nil, fmt.Errorf("ledger: unknown transfer kind %q", f.Kind)
		}
	}
	if !f.Since.IsZero() {
		af.TimestampMin = uint64(max(f.Since.UnixNano(), 1))
	}
	if !f.Until.IsZero() {
		if af.TimestampMax = uint64(max(f.Until.UnixNano()-1, 1)); af.TimestampMax < af.TimestampMin {
			return []Entry{}, nil
		}
	}
	if f.Before > 0 {
		if f.Before == 1 {
			return []Entry{}, nil
		}
		if af.TimestampMax == 0 || f.Before-1 < af.TimestampMax {
			af.TimestampMax = f.Before - 1
		}
		if af.TimestampMax < af.TimestampMin {
			return []Entry{}, nil
		}
	}
	ts, err := r.GetAccountTransfers(ctx, af)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(ts))
	for i, t := range ts {
		out[i] = entry(id, t)
	}
	return out, nil
}

func entry(self ID, t StoredTransfer) Entry {
	e := Entry{ID: t.ID, Timestamp: t.Timestamp, At: time.Unix(0, int64(t.Timestamp)).UTC(), Kind: kindName(t.Code),
		Op: int64(t.ID.Lo), Amount: int64(t.Amount), Stage: "posted"}
	other := t.Credit
	if t.Debit == self {
		e.Amount = -e.Amount
	} else {
		other = t.Debit
	}
	e.Posted = e.Amount
	switch {
	case t.Flags.Pending:
		e.Stage, e.Posted = "held", 0
	case t.Flags.PostPending:
		e.Stage = "settled"
	case t.Flags.VoidPending:
		e.Stage, e.Posted = "released", 0
	}
	if other.Hi == 0 {
		e.Other.System = systemKinds[other.Lo&0xff]
	} else {
		e.Other.User = int64(other.Lo)
	}
	// Game operations: game<<8 | leg, alone (ns 0) or in a namespace keyed by game.
	switch ns := t.ID.Hi >> 32; ns {
	case 0, nsSpend, nsTransfer, nsPrize:
		e.Game = Game(t.ID.Hi>>8) & MaxGame
	}
	return e
}
