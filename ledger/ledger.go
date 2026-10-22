// Package ledger owns all money movement. A Driver (TigerBeetle, Postgres) is the source of truth
// for balances; this package holds the money logic every driver shares.
package ledger

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// Currency is a balance a user plays with ("usd", "tickets", …). Each is its own driver ledger.
type Currency string

// CurrencyConfig is one currency of a brand.
type CurrencyConfig struct {
	Code      Currency
	Ledger    uint32 // unique per currency, > 0; never change it once money moved
	MinAmount int64  // smallest real-money amount a game may charge, in minor units; game limits can raise it, never lower it
	// Welcome is granted once per user on the first EnsureUser. It is play-only: a sale (Swap) or a
	// PvP entry (HoldPot) can never take the balance below it, so the grant can't be cashed out.
	Welcome int64
	// Decimals is display only: 2 = cents (1234 shows as 12.34); 0 = whole units (the default).
	// Every amount stays in minor units.
	Decimals uint8
	// Transferable lets users send this currency to each other (Transfer). Off by default, so real
	// money can't move between players unless the brand opts in (soft coins, gems).
	Transferable bool
}

// Account codes.
const (
	codeUser     uint16 = 1
	codeHouse    uint16 = 2
	codeFaucet   uint16 = 3
	codeInflow   uint16 = 4 // deposits enter the ledger from here
	codeExchange uint16 = 5
	codeGrant    uint16 = 6  // welcome grants come from here
	codeFloor    uint16 = 7  // play-only check: welcome amounts pass through and straight back
	codeRewards  uint16 = 8  // promotions are paid from here
	codePot      uint16 = 9  // PvP pots: entries wait here until the winner is paid
	codeAdjust   uint16 = 17 // admin balance adjustments come from and go to here
	codeSales    uint16 = 19 // spends (a shop item, an extra life) go here
)

// Transfer codes.
const (
	codeCharge     uint16 = 10
	codePayout     uint16 = 11
	codeTopUp      uint16 = 12
	codeDeposit    uint16 = 13
	codeWelcome    uint16 = 14
	codeSwap       uint16 = 15
	codeReward     uint16 = 16
	codeAdjustment uint16 = 18
	codeSpend      uint16 = 20
	codeTransfer   uint16 = 21
)

// Game is an ID space for one table of operation IDs: it namespaces the transfer ID, so row IDs
// from different tables never collide (a module with two such tables registers two). Each game picks
// its own constant, unique in the deployment, in 1..MaxGame; never reuse or change one. The
// platform's games use 1..FirstBrandGame-1, a brand's own games FirstBrandGame..MaxGame. Register it
// with RegisterGame so a duplicate fails at startup.
type Game uint64

const (
	// MaxGame keeps operation transfer IDs (game<<8 | leg) below the other namespaces (ns<<32).
	MaxGame Game = 1<<24 - 1
	// FirstBrandGame is the first ID for a brand's own games.
	FirstBrandGame Game = 1 << 20
)

// ErrBadGame: a game ID outside 1..MaxGame.
var ErrBadGame = errors.New("ledger: game ID outside 1..MaxGame")

func (g Game) check() error {
	if g == 0 || g > MaxGame {
		return fmt.Errorf("%w: %d", ErrBadGame, g)
	}
	return nil
}

// Legs of one operation.
const (
	legStake  uint64 = 1 // instant: posted amount; held games: the pending hold
	legPayout uint64 = 2
	legPost   uint64 = 3 // held games: posts the pending hold
	// Extra holds of one game (more amount added mid-game): hold k (1..MaxExtraHolds) is the pending
	// leg legExtra+k, posted by legExtraPost+k or voided by legExtraVoid+k.
	legExtra     uint64 = 3
	legExtraPost uint64 = 11
	legExtraVoid uint64 = 19
)

// MaxExtraHolds is how many extra amounts one held game can add (HoldExtra).
const MaxExtraHolds = 3

// Transfer ID namespaces (high 32 bits of ID.Hi) of everything that isn't a game operation.
const (
	nsDeposit  uint64 = 1
	nsWelcome  uint64 = 2
	nsSwap     uint64 = 3
	nsReward   uint64 = 4
	nsTopUp    uint64 = 5
	nsAdjust   uint64 = 6
	nsSpend    uint64 = 7 // keyed by game: ns<<32 | game<<8 | leg
	nsTransfer uint64 = 8 // keyed by game, as nsSpend
)

// System accounts, one of each per currency: ID (0, ledger<<8 | kind).
const (
	kindHouse uint64 = iota + 1
	kindPot
	kindExchange
	kindInflow
	kindFaucet
	kindGrant
	kindFloor
	kindRewards
	kindAdjust
	kindSales
)

// systemKinds names each system account, as SystemBalances keys them.
var systemKinds = map[uint64]string{kindHouse: "house", kindPot: "pot", kindExchange: "exchange",
	kindInflow: "inflow", kindFaucet: "faucet", kindGrant: "grant", kindFloor: "floor", kindRewards: "rewards",
	kindAdjust: "adjust", kindSales: "sales"}

var ErrInsufficientFunds = errors.New("ledger: insufficient funds")

// ErrNotTransferable: a Transfer in a currency without Transferable.
var ErrNotTransferable = errors.New("ledger: currency not transferable")

// ErrAlreadyFailed: a retry of a transfer ID the driver refused before (for the balance, say). The
// ID stays refused for good, even once the reason is gone; a new attempt needs a new ID.
var ErrAlreadyFailed = errors.New("ledger: transfer ID already failed")

type Ledger struct {
	d     Driver
	cur   map[Currency]CurrencyConfig
	order []Currency

	mu    sync.Mutex
	games map[Game]string
}

// RegisterGame claims g for the game called name: a second game with the same ID, or the same game
// with a second ID, is refused. A game's Register calls it before moving any money.
func (l *Ledger) RegisterGame(g Game, name string) error {
	if err := g.check(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.games == nil {
		l.games = map[Game]string{}
	}
	if other, ok := l.games[g]; ok && other != name {
		return fmt.Errorf("ledger: game %d is %q, not %q", g, other, name)
	}
	for other, n := range l.games {
		if n == name && other != g {
			return fmt.Errorf("ledger: game %q is already %d", name, other)
		}
	}
	l.games[g] = name
	return nil
}

// New checks the currencies (the first is the default) and returns the ledger. Call Init before use.
func New(d Driver, currencies ...CurrencyConfig) (*Ledger, error) {
	if len(currencies) == 0 {
		return nil, errors.New("ledger: no currencies")
	}
	l := &Ledger{d: d, cur: map[Currency]CurrencyConfig{}}
	ledgers := map[uint32]bool{}
	for _, c := range currencies {
		switch {
		case c.Code == "":
			return nil, errors.New("ledger: currency without a code")
		case c.Ledger == 0 || ledgers[c.Ledger]:
			return nil, fmt.Errorf("ledger: currency %q: ledger %d is 0 or taken", c.Code, c.Ledger)
		case l.cur[c.Code].Code != "":
			return nil, fmt.Errorf("ledger: currency %q twice", c.Code)
		case c.MinAmount <= 0 || c.Welcome < 0:
			return nil, fmt.Errorf("ledger: currency %q: min amount must be > 0, welcome >= 0", c.Code)
		}
		ledgers[c.Ledger] = true
		l.cur[c.Code] = c
		l.order = append(l.order, c.Code)
	}
	return l, nil
}

// Driver is the driver the ledger runs on.
func (l *Ledger) Driver() Driver { return l.d }

// Currencies lists every currency, in configured order.
func (l *Ledger) Currencies() []Currency { return append([]Currency(nil), l.order...) }

// Default is the first currency: what an operation without one uses.
func (l *Ledger) Default() Currency { return l.order[0] }

// Valid reports whether c is a configured currency.
func (l *Ledger) Valid(c Currency) bool { _, ok := l.cur[c]; return ok }

// Decimals is how many decimals c is shown with (display only), 0 for an unknown currency.
func (l *Ledger) Decimals(c Currency) uint8 { return l.cur[c].Decimals }

// MinAmount is the smallest real-money amount a game may charge in c, 0 for an unknown currency.
func (l *Ledger) MinAmount(c Currency) int64 { return l.cur[c].MinAmount }

func system(c CurrencyConfig, kind uint64) ID { return ID{0, uint64(c.Ledger)<<8 | kind} }

// userAccountID: (ledger, user ID).
func userAccountID(c CurrencyConfig, userID int64) ID { return ID{uint64(c.Ledger), uint64(userID)} }

// opTransferID is deterministic: the same operation leg always gets the same ID, so retries can't double-move money.
func opTransferID(g Game, leg uint64, opID int64) ID { return ID{uint64(g)<<8 | leg, uint64(opID)} }

// nsOpID is a game's operation ID in namespace ns, apart from the game legs that pots and holds
// share: ns<<32 | game<<8 | leg (Game <= MaxGame keeps it below bit 32).
func nsOpID(ns uint64, g Game, leg uint64, opID int64) ID {
	return ID{ns<<32 | uint64(g)<<8 | leg, uint64(opID)}
}

func (l *Ledger) currency(c Currency) (CurrencyConfig, error) {
	cur, ok := l.cur[c]
	if !ok {
		return cur, fmt.Errorf("ledger: unknown currency %q", c)
	}
	return cur, nil
}

// Init creates the system accounts. Safe to call on every start.
func (l *Ledger) Init(ctx context.Context) error {
	var accounts []Account
	for _, code := range l.order {
		c := l.cur[code]
		for kind, acc := range map[uint64]uint16{kindHouse: codeHouse, kindPot: codePot, kindExchange: codeExchange,
			kindInflow: codeInflow, kindFaucet: codeFaucet, kindGrant: codeGrant, kindFloor: codeFloor, kindRewards: codeRewards,
			kindAdjust: codeAdjust, kindSales: codeSales} {
			accounts = append(accounts, Account{ID: system(c, kind), Ledger: c.Ledger, Code: acc, NoOverdraft: kind == kindPot})
		}
	}
	return l.d.CreateAccounts(ctx, accounts)
}

// EnsureUser creates the user's accounts and grants the welcome amounts once (the transfer ID is
// the user ID, so every later login is a no-op). The driver itself rejects any debit beyond the balance.
func (l *Ledger) EnsureUser(ctx context.Context, userID int64) error {
	var accounts []Account
	var grants []Transfer
	for _, code := range l.order {
		c := l.cur[code]
		accounts = append(accounts, Account{ID: userAccountID(c, userID), Ledger: c.Ledger, Code: codeUser, NoOverdraft: true})
		if c.Welcome > 0 {
			grants = append(grants, Transfer{ID: ID{nsWelcome<<32 | uint64(c.Ledger), uint64(userID)},
				Debit: system(c, kindGrant), Credit: userAccountID(c, userID), Amount: uint64(c.Welcome),
				Ledger: c.Ledger, Code: codeWelcome})
		}
	}
	if err := l.d.CreateAccounts(ctx, accounts); err != nil || len(grants) == 0 {
		return err
	}
	return l.createTransfers(ctx, grants)
}

// Balances is what the user can spend, per currency.
type Balances map[Currency]int64

// Balances: posted credits minus posted and pending debits, per currency.
func (l *Ledger) Balances(ctx context.Context, userID int64) (Balances, error) {
	ids := make([]ID, len(l.order))
	byID := map[ID]Currency{}
	for i, code := range l.order {
		ids[i] = userAccountID(l.cur[code], userID)
		byID[ids[i]] = code
	}
	accs, err := l.d.LookupAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(accs) == 0 {
		return nil, fmt.Errorf("ledger: no account for user %d", userID)
	}
	b := Balances{}
	for _, a := range accs {
		b[byID[a.ID]] = a.CreditsPosted - a.DebitsPosted - a.DebitsPending
	}
	return b, nil
}

// RewardsPaid is what c's rewards account has paid out net, all time: rewards and bonuses out,
// given-up bonuses back in.
func (l *Ledger) RewardsPaid(ctx context.Context, c Currency) (int64, error) {
	cur, err := l.currency(c)
	if err != nil {
		return 0, err
	}
	accs, err := l.d.LookupAccounts(ctx, []ID{system(cur, kindRewards)})
	if err != nil {
		return 0, err
	}
	if len(accs) == 0 {
		return 0, errors.New("ledger: no rewards account")
	}
	return accs[0].DebitsPosted - accs[0].CreditsPosted, nil
}

// SystemBalances is credits_posted - debits_posted of each of c's system accounts, keyed "house",
// "pot", "exchange", "inflow", "faucet", "grant", "floor", "rewards" and "adjust". Accounts that pay
// out (inflow, faucet, grant, rewards, adjust when it credits users) go negative.
func (l *Ledger) SystemBalances(ctx context.Context, c Currency) (map[string]int64, error) {
	cur, err := l.currency(c)
	if err != nil {
		return nil, err
	}
	ids := make([]ID, 0, len(systemKinds))
	byID := map[ID]string{}
	for kind, name := range systemKinds {
		id := system(cur, kind)
		ids = append(ids, id)
		byID[id] = name
	}
	accs, err := l.d.LookupAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(accs) != len(ids) {
		return nil, fmt.Errorf("ledger: %d of %d system accounts of %q found", len(accs), len(ids), c)
	}
	out := make(map[string]int64, len(accs))
	for _, a := range accs {
		out[byID[a.ID]] = a.CreditsPosted - a.DebitsPosted
	}
	return out, nil
}

// SettleInstant moves money for an operation whose outcome is known at once: amount user ->
// house, then payout house -> user if payout > 0, as one atomic linked batch. Retrying with the
// same operation ID is safe. A demo operation (amount 0) moves nothing.
func (l *Ledger) SettleInstant(ctx context.Context, g Game, c Currency, opID, userID, amount, payout int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	user := userAccountID(cur, userID)
	transfers := []Transfer{{ID: opTransferID(g, legStake, opID), Debit: user, Credit: system(cur, kindHouse),
		Amount: uint64(amount), Ledger: cur.Ledger, Code: codeCharge}}
	if payout > 0 {
		transfers[0].Flags.Linked = true
		transfers = append(transfers, Transfer{ID: opTransferID(g, legPayout, opID), Debit: system(cur, kindHouse),
			Credit: user, Amount: uint64(payout), Ledger: cur.Ledger, Code: codePayout})
	}
	return l.createTransfers(ctx, transfers)
}

// Hold reserves the amount of a multi-step game as a pending transfer user -> house. The balance
// drops immediately; the money moves for good only in SettleHeld. Safe to repeat. A demo game
// (amount 0) holds nothing.
func (l *Ledger) Hold(ctx context.Context, g Game, c Currency, opID, userID, amount int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	return l.createTransfers(ctx, []Transfer{{ID: opTransferID(g, legStake, opID), Debit: userAccountID(cur, userID),
		Credit: system(cur, kindHouse), Amount: uint64(amount), Ledger: cur.Ledger, Code: codeCharge,
		Flags: Flags{Pending: true}}})
}

// SettleHeld finishes a held game: posts the amount to the house and, if payout > 0,
// pays house -> user in the same atomic linked batch. Safe to repeat. Demo games move nothing.
func (l *Ledger) SettleHeld(ctx context.Context, g Game, c Currency, opID, userID, amount, payout int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	user := userAccountID(cur, userID)
	transfers := []Transfer{{ID: opTransferID(g, legPost, opID), PendingID: opTransferID(g, legStake, opID),
		Debit: user, Credit: system(cur, kindHouse), Amount: uint64(amount), Ledger: cur.Ledger, Code: codeCharge,
		Flags: Flags{PostPending: true}}}
	if payout > 0 {
		transfers[0].Flags.Linked = true
		transfers = append(transfers, Transfer{ID: opTransferID(g, legPayout, opID), Debit: system(cur, kindHouse),
			Credit: user, Amount: uint64(payout), Ledger: cur.Ledger, Code: codePayout})
	}
	return l.createTransfers(ctx, transfers)
}

// HoldExtra adds amount k (1..MaxExtraHolds) to a held game (more put in mid-game) as another
// pending transfer user -> house. Its ID comes from (operation, k), so a retry holds it once.
// The driver remembers a failed ID: once refused for the balance, hold k stays refused
// (ErrInsufficientFunds) for that game even after a top-up. A demo game (amount 0) holds nothing.
func (l *Ledger) HoldExtra(ctx context.Context, g Game, c Currency, opID, userID int64, k int, amount int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	if k < 1 || k > MaxExtraHolds {
		return fmt.Errorf("ledger: extra hold %d out of range", k)
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	res, err := l.d.CreateTransfers(ctx, []Transfer{{ID: opTransferID(g, legExtra+uint64(k), opID),
		Debit: userAccountID(cur, userID), Credit: system(cur, kindHouse), Amount: uint64(amount),
		Ledger: cur.Ledger, Code: codeCharge, Flags: Flags{Pending: true}}})
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Result {
		case ResultExists:
		case ResultExceedsCredits, ResultIDAlreadyFailed:
			return ErrInsufficientFunds
		default:
			return fmt.Errorf("ledger: extra hold %d: %s", k, r.Result)
		}
	}
	return nil
}

// SettleHolds finishes a held game with extra amounts: posts the main amount and every extra hold
// (extra[k-1] for hold k, 0 = not held) and pays house -> user, all in one atomic linked batch.
// Then it voids the extra holds the game didn't use, in case one was held by an action whose
// commit failed (no such hold: nothing to do). Safe to repeat. Demo games move nothing.
func (l *Ledger) SettleHolds(ctx context.Context, g Game, c Currency, opID, userID, amount int64, extra []int64, payout int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	if len(extra) > MaxExtraHolds {
		return fmt.Errorf("ledger: %d extra holds", len(extra))
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	user, house := userAccountID(cur, userID), system(cur, kindHouse)
	post := func(id, pending ID, amount int64) Transfer {
		return Transfer{ID: id, PendingID: pending, Debit: user, Credit: house, Amount: uint64(amount),
			Ledger: cur.Ledger, Code: codeCharge, Flags: Flags{PostPending: true}}
	}
	transfers := []Transfer{post(opTransferID(g, legPost, opID), opTransferID(g, legStake, opID), amount)}
	var unused []Transfer
	for k := 1; k <= MaxExtraHolds; k++ {
		pending := opTransferID(g, legExtra+uint64(k), opID)
		if k <= len(extra) && extra[k-1] > 0 {
			transfers = append(transfers, post(opTransferID(g, legExtraPost+uint64(k), opID), pending, extra[k-1]))
			continue
		}
		unused = append(unused, Transfer{ID: opTransferID(g, legExtraVoid+uint64(k), opID), PendingID: pending,
			Debit: user, Credit: house, Ledger: cur.Ledger, Code: codeCharge, Flags: Flags{VoidPending: true}})
	}
	if payout > 0 {
		transfers = append(transfers, Transfer{ID: opTransferID(g, legPayout, opID), Debit: house, Credit: user,
			Amount: uint64(payout), Ledger: cur.Ledger, Code: codePayout})
	}
	link(transfers)
	if err := l.createTransfers(ctx, transfers); err != nil {
		return err
	}
	if len(unused) == 0 {
		return nil
	}
	res, err := l.d.CreateTransfers(ctx, unused) // independent: most were never held
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Result {
		// Never held (IDAlreadyFailed: an earlier settle found nothing to void either), or voided already.
		case ResultExists, ResultPendingNotFound, ResultIDAlreadyFailed, ResultPendingAlreadyVoided:
		default:
			return fmt.Errorf("ledger: void extra hold %d: %s", r.Index, r.Result)
		}
	}
	return nil
}

// TopUp credits a user from c's faucet. Dev/testing only; not idempotent.
func (l *Ledger) TopUp(ctx context.Context, c Currency, userID, amount int64) error {
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	var b [8]byte
	rand.Read(b[:])
	return l.createTransfers(ctx, []Transfer{{ID: ID{nsTopUp << 32, binary.LittleEndian.Uint64(b[:])},
		Debit: system(cur, kindFaucet), Credit: userAccountID(cur, userID), Amount: uint64(amount),
		Ledger: cur.Ledger, Code: codeTopUp}})
}

// Deposit credits a confirmed deposit in c. The transfer ID comes from the deposit row ID, so
// redelivered webhooks can't credit twice.
func (l *Ledger) Deposit(ctx context.Context, c Currency, depositID, userID, amount int64) error {
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	return l.createTransfers(ctx, []Transfer{{ID: ID{nsDeposit << 32, uint64(depositID)},
		Debit: system(cur, kindInflow), Credit: userAccountID(cur, userID), Amount: uint64(amount),
		Ledger: cur.Ledger, Code: codeDeposit}})
}

// Reward pays a claim in c from its rewards account. The transfer ID comes from the claim row, so
// a retried claim can't pay twice.
func (l *Ledger) Reward(ctx context.Context, c Currency, claimID, userID, amount int64) error {
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	return l.createTransfers(ctx, []Transfer{{ID: ID{nsReward << 32, uint64(claimID)},
		Debit: system(cur, kindRewards), Credit: userAccountID(cur, userID), Amount: uint64(amount),
		Ledger: cur.Ledger, Code: codeReward}})
}

// Adjust credits (amount > 0) or debits (amount < 0) a user in c from/to c's adjust account. The
// transfer ID comes from the adjustment row ID, so a retry can't move money twice. A debit can't
// overdraw the user (ErrInsufficientFunds); its retry then returns ErrAlreadyFailed. amount 0 is
// an error.
func (l *Ledger) Adjust(ctx context.Context, c Currency, adjustmentID, userID, amount int64) error {
	if amount == 0 {
		return errors.New("ledger: zero adjustment")
	}
	cur, err := l.currency(c)
	if err != nil {
		return err
	}
	debit, credit := system(cur, kindAdjust), userAccountID(cur, userID)
	if amount < 0 {
		debit, credit, amount = credit, debit, -amount // -MinInt64 stays out of range: createTransfers refuses it
	}
	return l.createTransfers(ctx, []Transfer{{ID: ID{nsAdjust << 32, uint64(adjustmentID)},
		Debit: debit, Credit: credit, Amount: uint64(amount), Ledger: cur.Ledger, Code: codeAdjustment}})
}

// Spend charges a user for something the brand sells (a shop item, an extra life): user -> c's
// sales account. The transfer ID comes from (g, opID), so a retry charges once; a refused spend
// stays refused (ErrAlreadyFailed). The welcome grant may be spent: spending is play, not cash-out.
// Amount 0 moves nothing; a negative amount is an error.
func (l *Ledger) Spend(ctx context.Context, g Game, c Currency, opID, userID, amount int64) error {
	if err := g.check(); err != nil {
		return err
	}
	if amount < 0 {
		return fmt.Errorf("ledger: negative spend %d", amount)
	}
	cur, err := l.currency(c)
	if err != nil || amount == 0 {
		return err
	}
	return l.createTransfers(ctx, []Transfer{{ID: nsOpID(nsSpend, g, 1, opID), Debit: userAccountID(cur, userID),
		Credit: system(cur, kindSales), Amount: uint64(amount), Ledger: cur.Ledger, Code: codeSpend}})
}

// Transfer sends amount of c from one user to another (a gift, a trade), only in a Transferable
// currency (else ErrNotTransferable). The transfer ID comes from (g, opID), so a retry sends once.
// It's a cash-out path (farm welcome grants on many accounts, send them to one), so the grant stays
// play-only as in HoldPot: in a currency with a welcome grant, a linked pair from -> floor -> from
// of Welcome follows, and the batch fails (ErrInsufficientFunds) if less than that would be left.
// Amount 0 moves nothing; a negative amount or from == to is an error.
func (l *Ledger) Transfer(ctx context.Context, g Game, c Currency, opID, from, to, amount int64) error {
	if err := g.check(); err != nil {
		return err
	}
	cur, err := l.currency(c)
	switch {
	case err != nil:
		return err
	case !cur.Transferable:
		return fmt.Errorf("%w: %q", ErrNotTransferable, c)
	case from == to:
		return fmt.Errorf("ledger: transfer from user %d to itself", from)
	case amount < 0:
		return fmt.Errorf("ledger: negative transfer %d", amount)
	case amount == 0:
		return nil
	}
	sender := userAccountID(cur, from)
	transfers := []Transfer{{ID: nsOpID(nsTransfer, g, 1, opID), Debit: sender, Credit: userAccountID(cur, to),
		Amount: uint64(amount), Ledger: cur.Ledger, Code: codeTransfer}}
	if cur.Welcome > 0 {
		transfers = append(transfers,
			Transfer{ID: nsOpID(nsTransfer, g, 2, opID), Debit: sender, Credit: system(cur, kindFloor),
				Amount: uint64(cur.Welcome), Ledger: cur.Ledger, Code: codeFloor},
			Transfer{ID: nsOpID(nsTransfer, g, 3, opID), Debit: system(cur, kindFloor), Credit: sender,
				Amount: uint64(cur.Welcome), Ledger: cur.Ledger, Code: codeFloor})
		link(transfers)
	}
	return l.createTransfers(ctx, transfers)
}

// Swap exchanges currencies for the user: `from` user -> exchange and exchange -> `to` user, linked,
// so every leg happens or none (transfers stay within one ledger). The transfer IDs come from the
// swap row ID, so retries can't swap twice.
//
// bonus (a buy) adds a bonus from `to`'s rewards account.
//
// Selling a currency with a welcome grant keeps the grant play-only, checked by the driver itself
// (no app-side balance check): right after the sale, Welcome moves user -> floor and straight back
// in the same linked batch. If the sale left less than that, the first of them exceeds the balance
// and the whole batch fails (ErrInsufficientFunds); otherwise the pair nets to nothing.
//
// forfeit > 0 (a sale that gives a locked bonus up) gives up the bonus and everything won with it:
// while the welcome amount sits in the floor account, a balancing debit user -> rewards takes all
// that is left, so the player ends with exactly Welcome plus the sale's proceeds. The driver
// computes that amount atomically (no app-side balance read, so a concurrent win can't slip
// through); forfeit is only a marker, the amount moved is the transfer's.
func (l *Ledger) Swap(ctx context.Context, swapID, userID int64, from Currency, fromAmount int64, to Currency, toAmount, bonus, forfeit int64) error {
	f, err := l.currency(from)
	if err != nil {
		return err
	}
	t, err := l.currency(to)
	if err != nil {
		return err
	}
	id := func(leg uint64) ID { return ID{nsSwap<<32 | leg, uint64(swapID)} }
	seller := userAccountID(f, userID)
	transfers := []Transfer{{ID: id(1), Debit: seller, Credit: system(f, kindExchange),
		Amount: uint64(fromAmount), Ledger: f.Ledger, Code: codeSwap}}
	if f.Welcome > 0 {
		transfers = append(transfers, Transfer{ID: id(3), Debit: seller, Credit: system(f, kindFloor),
			Amount: uint64(f.Welcome), Ledger: f.Ledger, Code: codeFloor})
		if forfeit > 0 {
			transfers = append(transfers, Transfer{ID: id(6), Debit: seller, Credit: system(f, kindRewards),
				Amount: AmountMax, Ledger: f.Ledger, Code: codeReward, Flags: Flags{BalancingDebit: true}})
		}
		transfers = append(transfers, Transfer{ID: id(4), Debit: system(f, kindFloor), Credit: seller,
			Amount: uint64(f.Welcome), Ledger: f.Ledger, Code: codeFloor})
	}
	buyer := userAccountID(t, userID)
	transfers = append(transfers, Transfer{ID: id(2), Debit: system(t, kindExchange), Credit: buyer,
		Amount: uint64(toAmount), Ledger: t.Ledger, Code: codeSwap})
	if bonus > 0 {
		transfers = append(transfers, Transfer{ID: id(5), Debit: system(t, kindRewards), Credit: buyer,
			Amount: uint64(bonus), Ledger: t.Ledger, Code: codeReward})
	}
	link(transfers)
	return l.createTransfers(ctx, transfers)
}

// link chains every transfer to the next, so the batch applies whole or not at all.
func link(transfers []Transfer) {
	for i := range transfers[:len(transfers)-1] {
		transfers[i].Flags.Linked = true
	}
}

// createTransfers treats "already exists" as success (idempotent retry), maps overdraft to
// ErrInsufficientFunds and a retry of a refused ID to ErrAlreadyFailed.
func (l *Ledger) createTransfers(ctx context.Context, transfers []Transfer) error {
	for _, t := range transfers {
		if t.Amount > math.MaxInt64 && t.Amount != AmountMax {
			return fmt.Errorf("ledger: amount %d out of range", t.Amount)
		}
	}
	res, err := l.d.CreateTransfers(ctx, transfers)
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Result {
		case ResultExists, ResultLinkedEventFailed:
			// Exists: an earlier attempt already applied it. LinkedEventFailed: see the failing event.
		case ResultExceedsCredits:
			return ErrInsufficientFunds
		case ResultIDAlreadyFailed:
			return fmt.Errorf("ledger: transfer %d: %w (%s)", r.Index, ErrAlreadyFailed, r.Result)
		default:
			return fmt.Errorf("ledger: transfer %d: %s", r.Index, r.Result)
		}
	}
	return nil
}
