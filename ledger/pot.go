package ledger

import (
	"context"
	"fmt"
)

// PvP pots: every entry is a pending transfer user -> pot, so the stake leaves the balance at once.
// Settling posts them all and pays the winners from the pot; the house only takes Fee of the pot.

// FeeBP is the house's cut of a pot, in basis points (5%).
const FeeBP = 500

// Fee is the house's cut of a pot, rounded up so it is never below FeeBP.
func Fee(pot int64) int64 { return (pot*FeeBP + 9999) / 10000 }

// Pot legs. Entry legs use the entry ID, pot legs the pot ID. Leg 8+i is the prize of winner i
// (i > 0 only for lottery prizes).
const (
	legPotHold   uint64 = 1
	legPotPost   uint64 = 2
	legPotVoid   uint64 = 3
	legPotFee    uint64 = 4
	legPotFloor  uint64 = 5 // welcome amount out to the floor...
	legPotBack   uint64 = 6 // ...and back
	legPotShare  uint64 = 7 // SettlePool: a winning entry's share, keyed by entry ID
	legPotWinner uint64 = 8
)

// PotEntry is one player's stake in a pot; ID is unique per game.
type PotEntry struct{ ID, UserID, Amount int64 }

// PotWin is a payout from the pot.
type PotWin struct{ UserID, Amount int64 }

// HoldPot holds an entry's stake. In a currency with a welcome grant the grant stays play-only: a
// linked pair user -> floor -> user of Welcome after the hold fails the whole batch if the balance
// left is lower. Safe to repeat; a refused entry stays refused (ErrInsufficientFunds), as HoldExtra.
func (l *Ledger) HoldPot(ctx context.Context, g Game, c Currency, e PotEntry) error {
	if err := g.check(); err != nil {
		return err
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	user := userAccountID(cur, e.UserID)
	id := func(leg uint64) ID { return betTransferID(g, leg, e.ID) }
	transfers := []Transfer{{ID: id(legPotHold), Debit: user, Credit: system(cur, kindPot),
		Amount: uint64(e.Amount), Ledger: cur.Ledger, Code: codeStake, Flags: Flags{Pending: true}}}
	if cur.Welcome > 0 {
		transfers = append(transfers,
			Transfer{ID: id(legPotFloor), Debit: user, Credit: system(cur, kindFloor),
				Amount: uint64(cur.Welcome), Ledger: cur.Ledger, Code: codeFloor},
			Transfer{ID: id(legPotBack), Debit: system(cur, kindFloor), Credit: user,
				Amount: uint64(cur.Welcome), Ledger: cur.Ledger, Code: codeFloor})
		link(transfers)
	}
	res, err := l.d.CreateTransfers(ctx, transfers)
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Result {
		case ResultExists, ResultLinkedEventFailed:
		case ResultExceedsCredits, ResultIDAlreadyFailed:
			return ErrInsufficientFunds
		default:
			return fmt.Errorf("ledger: hold pot entry %d: %s", e.ID, r.Result)
		}
	}
	return nil
}

// SettlePot posts every entry, pays the fee to the house and the winners from the pot, in one
// linked batch (nothing is sent if the winners don't add up to the pot less Fee). Safe to repeat.
func (l *Ledger) SettlePot(ctx context.Context, g Game, c Currency, potID int64, entries []PotEntry, winners []PotWin) (int64, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	var pot, won int64
	for _, e := range entries {
		pot += e.Amount
	}
	for _, w := range winners {
		won += w.Amount
	}
	fee := Fee(pot)
	if len(winners) == 0 || won != pot-fee {
		return 0, fmt.Errorf("ledger: pot %d: winners get %d, want %d", potID, won, pot-fee)
	}
	cur, err := l.currency(c)
	if err != nil {
		return 0, err
	}
	transfers := posts(g, cur, entries)
	transfers = append(transfers, Transfer{ID: betTransferID(g, legPotFee, potID), Debit: system(cur, kindPot),
		Credit: system(cur, kindHouse), Amount: uint64(fee), Ledger: cur.Ledger, Code: codeStake})
	for i, w := range winners {
		transfers = append(transfers, Transfer{ID: betTransferID(g, legPotWinner+uint64(i), potID),
			Debit: system(cur, kindPot), Credit: userAccountID(cur, w.UserID), Amount: uint64(w.Amount),
			Ledger: cur.Ledger, Code: codePayout})
	}
	link(transfers)
	return fee, l.createTransfers(ctx, transfers)
}

// posts posts every entry's hold.
func posts(g Game, cur CurrencyConfig, entries []PotEntry) []Transfer {
	var out []Transfer
	for _, e := range entries {
		out = append(out, Transfer{ID: betTransferID(g, legPotPost, e.ID), PendingID: betTransferID(g, legPotHold, e.ID),
			Debit: userAccountID(cur, e.UserID), Credit: system(cur, kindPot), Amount: uint64(e.Amount),
			Ledger: cur.Ledger, Code: codeStake, Flags: Flags{PostPending: true}})
	}
	return out
}

// VoidPot gives entries back, each void independent: most callers don't know which were held.
func (l *Ledger) VoidPot(ctx context.Context, g Game, c Currency, entries []PotEntry) error {
	if err := g.check(); err != nil {
		return err
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	var transfers []Transfer
	for _, e := range entries {
		transfers = append(transfers, Transfer{ID: betTransferID(g, legPotVoid, e.ID), PendingID: betTransferID(g, legPotHold, e.ID),
			Debit: userAccountID(cur, e.UserID), Credit: system(cur, kindPot), Ledger: cur.Ledger, Code: codeStake,
			Flags: Flags{VoidPending: true}})
	}
	res, err := l.d.CreateTransfers(ctx, transfers)
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Result {
		case ResultExists, ResultPendingNotFound, ResultIDAlreadyFailed, ResultPendingAlreadyVoided:
		default:
			return fmt.Errorf("ledger: void pot entry %d: %s", entries[r.Index].ID, r.Result)
		}
	}
	return nil
}

// PotShare is a winning entry's payout from a pool; ID and UserID are the entry's.
type PotShare struct{ EntryID, UserID, Amount int64 }

// poolChunk is how many transfers SettlePool sends per batch (TigerBeetle's limit is 8189).
var poolChunk = 8000

// SettlePool is SettlePot for big pools (Predict): same checks, but sent in unlinked chunks, each
// transfer keyed by entry so a crash between chunks is finished by repeating the call.
func (l *Ledger) SettlePool(ctx context.Context, g Game, c Currency, potID int64, entries []PotEntry, shares []PotShare) (int64, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	var pot, won int64
	for _, e := range entries {
		pot += e.Amount
	}
	for _, s := range shares {
		won += s.Amount
	}
	fee := Fee(pot)
	if len(shares) == 0 || won != pot-fee {
		return 0, fmt.Errorf("ledger: pot %d: winners get %d, want %d", potID, won, pot-fee)
	}
	// Shares must be sane before anything is sent: an unlinked batch can't be undone.
	in := map[int64]bool{}
	for _, e := range entries {
		in[e.ID] = true
	}
	seen := map[int64]bool{}
	for _, sh := range shares {
		if sh.Amount < 0 || seen[sh.EntryID] || !in[sh.EntryID] {
			return 0, fmt.Errorf("ledger: pot %d: bad share for entry %d", potID, sh.EntryID)
		}
		seen[sh.EntryID] = true
	}
	cur, err := l.currency(c)
	if err != nil {
		return 0, err
	}
	// Every post first: the fee and shares are paid from the shared pot account, so they must not
	// apply while a post (a voided or missing hold) failed. Unlinked: a pool is too big for one
	// batch, so each transfer has its own deterministic ID and a retry skips what exists.
	payouts := []Transfer{{ID: betTransferID(g, legPotFee, potID), Debit: system(cur, kindPot),
		Credit: system(cur, kindHouse), Amount: uint64(fee), Ledger: cur.Ledger, Code: codeStake}}
	for _, s := range shares {
		if s.Amount == 0 {
			continue
		}
		payouts = append(payouts, Transfer{ID: betTransferID(g, legPotShare, s.EntryID), Debit: system(cur, kindPot),
			Credit: userAccountID(cur, s.UserID), Amount: uint64(s.Amount), Ledger: cur.Ledger, Code: codePayout})
	}
	for _, part := range [][]Transfer{posts(g, cur, entries), payouts} {
		for len(part) > 0 {
			n := min(poolChunk, len(part))
			if err := l.createTransfers(ctx, part[:n]); err != nil {
				return 0, err
			}
			part = part[n:]
		}
	}
	return fee, nil
}
