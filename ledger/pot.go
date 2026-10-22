package ledger

import (
	"context"
	"errors"
	"fmt"
)

// PvP pots: every entry is a pending transfer user -> pot, so the amount leaves the balance at once.
// Settling posts them all and pays the winners from the pot; the house only takes Fee of the pot.

// FeeBP is the house's cut of a pot, in basis points (5%).
const FeeBP = 500

// Fee is the house's cut of a pot, rounded up so it is never below FeeBP.
func Fee(pot int64) int64 { return FeeOf(pot, FeeBP) }

// FeeOf is the house's cut of a pot at bp basis points, rounded up. A product that sets its own cut
// per pot (SettlePotFee, SettlePoolFee) passes bp; 0 is no fee.
func FeeOf(pot, bp int64) int64 { return (pot*bp + 9999) / 10000 }

// ErrBadFee: a fee outside 0..10_000 basis points.
var ErrBadFee = errors.New("ledger: fee basis points outside 0..10000")

func checkFeeBP(bp int64) error {
	if bp < 0 || bp > 10_000 {
		return fmt.Errorf("%w: %d", ErrBadFee, bp)
	}
	return nil
}

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

// MaxPotWinners is the most winners one pot pays (SettlePot): winner i's prize is leg
// legPotWinner+i, which has to fit the transfer ID's 8-bit leg.
const MaxPotWinners = 255 - int(legPotWinner)

// ErrTooManyWinners: a SettlePot with more than MaxPotWinners winners.
var ErrTooManyWinners = errors.New("ledger: too many pot winners")

// PotEntry is one player's amount in a pot; ID is unique per game.
type PotEntry struct{ ID, UserID, Amount int64 }

// PotWin is a payout from the pot.
type PotWin struct{ UserID, Amount int64 }

// HoldPot holds an entry's amount. In a currency with a welcome grant the grant stays play-only: a
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
	id := func(leg uint64) ID { return opTransferID(g, leg, e.ID) }
	transfers := []Transfer{{ID: id(legPotHold), Debit: user, Credit: system(cur, kindPot),
		Amount: uint64(e.Amount), Ledger: cur.Ledger, Code: codeCharge, Flags: Flags{Pending: true}}}
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
	return l.SettlePotFee(ctx, g, c, potID, FeeBP, entries, winners)
}

// SettlePotFee is SettlePot with the house's cut set per pot: feeBP basis points (0..10_000), rounded
// up as FeeOf. Transfer IDs don't depend on feeBP.
func (l *Ledger) SettlePotFee(ctx context.Context, g Game, c Currency, potID, feeBP int64, entries []PotEntry, winners []PotWin) (int64, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	if err := checkFeeBP(feeBP); err != nil {
		return 0, err
	}
	if len(winners) > MaxPotWinners {
		return 0, fmt.Errorf("%w: pot %d: %d", ErrTooManyWinners, potID, len(winners))
	}
	var pot, won int64
	for _, e := range entries {
		pot += e.Amount
	}
	for _, w := range winners {
		won += w.Amount
	}
	fee := FeeOf(pot, feeBP)
	if len(winners) == 0 || won != pot-fee {
		return 0, fmt.Errorf("ledger: pot %d: winners get %d, want %d", potID, won, pot-fee)
	}
	cur, err := l.currency(c)
	if err != nil {
		return 0, err
	}
	transfers := posts(g, cur, entries)
	transfers = append(transfers, Transfer{ID: opTransferID(g, legPotFee, potID), Debit: system(cur, kindPot),
		Credit: system(cur, kindHouse), Amount: uint64(fee), Ledger: cur.Ledger, Code: codeCharge})
	for i, w := range winners {
		transfers = append(transfers, Transfer{ID: opTransferID(g, legPotWinner+uint64(i), potID),
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
		out = append(out, Transfer{ID: opTransferID(g, legPotPost, e.ID), PendingID: opTransferID(g, legPotHold, e.ID),
			Debit: userAccountID(cur, e.UserID), Credit: system(cur, kindPot), Amount: uint64(e.Amount),
			Ledger: cur.Ledger, Code: codeCharge, Flags: Flags{PostPending: true}})
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
		transfers = append(transfers, Transfer{ID: opTransferID(g, legPotVoid, e.ID), PendingID: opTransferID(g, legPotHold, e.ID),
			Debit: userAccountID(cur, e.UserID), Credit: system(cur, kindPot), Ledger: cur.Ledger, Code: codeCharge,
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
	return l.SettlePoolFee(ctx, g, c, potID, FeeBP, entries, shares)
}

// SettlePoolFee is SettlePool with the house's cut set per pot, as SettlePotFee.
func (l *Ledger) SettlePoolFee(ctx context.Context, g Game, c Currency, potID, feeBP int64, entries []PotEntry, shares []PotShare) (int64, error) {
	if err := g.check(); err != nil {
		return 0, err
	}
	if err := checkFeeBP(feeBP); err != nil {
		return 0, err
	}
	var pot, won int64
	for _, e := range entries {
		pot += e.Amount
	}
	for _, s := range shares {
		won += s.Amount
	}
	fee := FeeOf(pot, feeBP)
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
	payouts := []Transfer{{ID: opTransferID(g, legPotFee, potID), Debit: system(cur, kindPot),
		Credit: system(cur, kindHouse), Amount: uint64(fee), Ledger: cur.Ledger, Code: codeCharge}}
	for _, s := range shares {
		if s.Amount == 0 {
			continue
		}
		payouts = append(payouts, Transfer{ID: opTransferID(g, legPotShare, s.EntryID), Debit: system(cur, kindPot),
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
