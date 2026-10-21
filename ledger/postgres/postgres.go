// Package postgres is a ledger driver on PostgreSQL, for brands that don't run TigerBeetle. It
// keeps TigerBeetle's semantics (ledger.Driver) and passes the same conformance suite.
//
// A batch runs in one transaction: it locks every account it touches (in ID order), applies the
// transfers in memory with TigerBeetle's rules, then writes the changes. A CHECK constraint on
// ledger_accounts refuses an overdraft even if this code had a bug.
//
// ponytail: every bet locks its currency's house account row, so bets in one currency commit one at
// a time (roughly a few thousand per second). Shard the house account if a brand outgrows that.
package postgres

import (
	"context"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/postgres/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations is the driver's schema; run it before use (db.Migrate with table "goose_ledger").
//
//go:embed migrations/*.sql
var Migrations embed.FS

type Driver struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func New(pool *pgxpool.Pool) *Driver { return &Driver{pool: pool, q: store.New(pool)} }

func uuid(id ledger.ID) pgtype.UUID {
	u := pgtype.UUID{Valid: true}
	binary.BigEndian.PutUint64(u.Bytes[:8], id.Hi)
	binary.BigEndian.PutUint64(u.Bytes[8:], id.Lo)
	return u
}

func fromUUID(u pgtype.UUID) ledger.ID {
	if !u.Valid {
		return ledger.ID{}
	}
	return ledger.ID{Hi: binary.BigEndian.Uint64(u.Bytes[:8]), Lo: binary.BigEndian.Uint64(u.Bytes[8:])}
}

func uuids(ids []ledger.ID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = uuid(id)
	}
	return out
}

func (d *Driver) CreateAccounts(ctx context.Context, accounts []ledger.Account) error {
	for _, a := range accounts {
		if a.ID == (ledger.ID{}) || a.Ledger == 0 || a.Code == 0 {
			return fmt.Errorf("ledger/postgres: account %v: id, ledger and code must not be zero", a.ID)
		}
		n, err := d.q.InsertAccount(ctx, store.InsertAccountParams{ID: uuid(a.ID), Ledger: int64(a.Ledger),
			Code: int32(a.Code), NoOverdraft: a.NoOverdraft})
		if err != nil {
			return err
		}
		if n == 1 {
			continue
		}
		got, err := d.q.GetAccount(ctx, uuid(a.ID))
		if err != nil {
			return err
		}
		if got.Ledger != int64(a.Ledger) || got.Code != int32(a.Code) || got.NoOverdraft != a.NoOverdraft {
			return fmt.Errorf("ledger/postgres: account %v exists with different fields", a.ID)
		}
	}
	return nil
}

func (d *Driver) LookupAccounts(ctx context.Context, ids []ledger.ID) ([]ledger.AccountBalance, error) {
	rows, err := d.q.LookupAccounts(ctx, uuids(ids))
	if err != nil {
		return nil, err
	}
	out := make([]ledger.AccountBalance, len(rows))
	for i, r := range rows {
		out[i] = ledger.AccountBalance{ID: fromUUID(r.ID), Ledger: uint32(r.Ledger),
			DebitsPending: r.DebitsPending, DebitsPosted: r.DebitsPosted,
			CreditsPending: r.CreditsPending, CreditsPosted: r.CreditsPosted}
	}
	return out, nil
}

// Ping checks Postgres answers.
func (d *Driver) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

func (d *Driver) CreateTransfers(ctx context.Context, transfers []ledger.Transfer) ([]ledger.TransferResult, error) {
	if len(transfers) == 0 {
		return nil, nil
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	b := &batch{q: d.q.WithTx(tx), accounts: map[ledger.ID]*account{}, transfers: map[ledger.ID]*xfer{}, failed: map[ledger.ID]bool{}}
	if err := b.load(ctx, transfers); err != nil {
		return nil, err
	}
	results := b.run(transfers)
	if err := b.write(ctx); err != nil {
		return nil, err
	}
	return results, tx.Commit(ctx)
}

type account struct {
	store.LedgerAccount
	dirty bool
}

type xfer struct {
	t     ledger.Transfer // as stored: accounts filled in, the amount that moved
	state string          // pending transfers: pending | posted | voided
	isNew bool
	moved bool // state changed in this batch
}

// batch is one CreateTransfers call: locked accounts, the transfers and failed IDs it refers to,
// and an undo log so a broken linked chain leaves no trace.
type batch struct {
	q         *store.Queries
	accounts  map[ledger.ID]*account
	transfers map[ledger.ID]*xfer
	failed    map[ledger.ID]bool
	newFailed []store.InsertFailedParams
	undo      []func()
}

// load locks the accounts the transfers name, then reads the transfers and failed IDs they refer
// to. A post or void that names no accounts locks its pending transfer's, and then reads again.
func (b *batch) load(ctx context.Context, transfers []ledger.Transfer) error {
	var accs, ids []ledger.ID
	for _, t := range transfers {
		accs = append(accs, t.Debit, t.Credit)
		ids = append(ids, t.ID)
		if t.PendingID != (ledger.ID{}) {
			ids = append(ids, t.PendingID)
		}
	}
	if err := b.lock(ctx, accs); err != nil {
		return err
	}
	if err := b.read(ctx, ids); err != nil {
		return err
	}
	var more []ledger.ID
	for _, t := range transfers {
		if p := b.transfers[t.PendingID]; p != nil && (b.accounts[p.t.Debit] == nil || b.accounts[p.t.Credit] == nil) {
			more = append(more, p.t.Debit, p.t.Credit)
		}
	}
	if len(more) == 0 {
		return nil
	}
	if err := b.lock(ctx, more); err != nil {
		return err
	}
	return b.read(ctx, ids) // what was read before the lock may have moved since
}

func (b *batch) lock(ctx context.Context, ids []ledger.ID) error {
	var todo []ledger.ID
	for _, id := range ids {
		if id != (ledger.ID{}) && b.accounts[id] == nil && !slices.Contains(todo, id) {
			todo = append(todo, id)
		}
	}
	rows, err := b.q.LockAccounts(ctx, uuids(todo))
	for _, r := range rows {
		b.accounts[fromUUID(r.ID)] = &account{LedgerAccount: r}
	}
	return err
}

func (b *batch) read(ctx context.Context, ids []ledger.ID) error {
	rows, err := b.q.GetTransfers(ctx, uuids(ids))
	if err != nil {
		return err
	}
	for _, r := range rows {
		x := &xfer{t: ledger.Transfer{ID: fromUUID(r.ID), Debit: fromUUID(r.Debit), Credit: fromUUID(r.Credit),
			Amount: uint64(r.Amount), PendingID: fromUUID(r.PendingID), Ledger: uint32(r.Ledger), Code: uint16(r.Code),
			Flags: flags(r.Flags)}}
		if r.PendingState != nil {
			x.state = *r.PendingState
		}
		b.transfers[x.t.ID] = x
	}
	failed, err := b.q.GetFailed(ctx, uuids(ids))
	for _, id := range failed {
		b.failed[fromUUID(id)] = true
	}
	return err
}

func flags(n int32) ledger.Flags {
	return ledger.Flags{Linked: n&1 != 0, Pending: n&2 != 0, PostPending: n&4 != 0, VoidPending: n&8 != 0, BalancingDebit: n&16 != 0}
}

func bits(f ledger.Flags) int32 {
	var n int32
	for i, on := range []bool{f.Linked, f.Pending, f.PostPending, f.VoidPending, f.BalancingDebit} {
		if on {
			n |= 1 << i
		}
	}
	return n
}

// run applies the transfers chain by chain and returns the result of every one not ResultOK.
func (b *batch) run(transfers []ledger.Transfer) []ledger.TransferResult {
	var results []ledger.TransferResult
	for i := 0; i < len(transfers); {
		j := i
		for j < len(transfers)-1 && transfers[j].Flags.Linked {
			j++
		}
		mark := len(b.undo)
		failedAt, res := -1, ledger.ResultOK
		if transfers[j].Flags.Linked {
			failedAt, res = j, ledger.ResultLinkedChainOpen
		} else {
			for k := i; k <= j; k++ {
				if r := b.apply(transfers[k]); r != ledger.ResultOK {
					failedAt, res = k, r
					break
				}
			}
		}
		if failedAt >= 0 {
			for len(b.undo) > mark {
				b.undo[len(b.undo)-1]()
				b.undo = b.undo[:len(b.undo)-1]
			}
			for k := i; k <= j; k++ {
				r := ledger.ResultLinkedEventFailed
				if k == failedAt {
					r = res
				}
				results = append(results, ledger.TransferResult{Index: k, Result: r})
			}
			// As TigerBeetle: a failure that depends on state reserves the ID for good.
			if res == ledger.ResultExceedsCredits || res == ledger.ResultAccountNotFound || res == ledger.ResultPendingNotFound {
				id := transfers[failedAt].ID
				b.failed[id] = true
				b.newFailed = append(b.newFailed, store.InsertFailedParams{ID: uuid(id), Result: string(res)})
			}
		}
		i = j + 1
	}
	return results
}

func (b *batch) apply(t ledger.Transfer) ledger.Result {
	f := t.Flags
	modes := 0
	for _, on := range []bool{f.Pending, f.PostPending, f.VoidPending} {
		if on {
			modes++
		}
	}
	if t.ID == (ledger.ID{}) || modes > 1 || f.BalancingDebit && modes > 0 && !f.Pending {
		return ledger.ResultInvalid
	}
	if b.failed[t.ID] {
		return ledger.ResultIDAlreadyFailed
	}
	if e := b.transfers[t.ID]; e != nil {
		return exists(e.t, t)
	}
	if f.PostPending || f.VoidPending {
		return b.finish(t)
	}
	return b.move(t)
}

// exists compares a retry with the stored transfer. A post or void is identified by its pending
// transfer, so its amount isn't compared; a balancing transfer stored what it could move.
func exists(e, t ledger.Transfer) ledger.Result {
	finishing := t.Flags.PostPending || t.Flags.VoidPending
	same := func(stored, got ledger.ID) bool { return stored == got || finishing && got == (ledger.ID{}) }
	switch {
	case e.Flags != t.Flags, e.PendingID != t.PendingID, !same(e.Debit, t.Debit), !same(e.Credit, t.Credit),
		e.Ledger != t.Ledger && !(finishing && t.Ledger == 0), e.Code != t.Code && !(finishing && t.Code == 0),
		!finishing && !t.Flags.BalancingDebit && e.Amount != t.Amount,
		t.Flags.BalancingDebit && e.Amount > t.Amount:
		return ledger.ResultExistsDifferent
	}
	return ledger.ResultExists
}

// finish posts or voids a pending transfer.
func (b *batch) finish(t ledger.Transfer) ledger.Result {
	if t.PendingID == (ledger.ID{}) {
		return ledger.ResultInvalid
	}
	p := b.transfers[t.PendingID]
	switch {
	case p == nil:
		return ledger.ResultPendingNotFound
	case !p.t.Flags.Pending:
		return ledger.ResultPendingNotPending
	case p.state == "posted":
		return ledger.ResultPendingAlreadyPosted
	case p.state == "voided":
		return ledger.ResultPendingAlreadyVoided
	case t.Debit != (ledger.ID{}) && t.Debit != p.t.Debit, t.Credit != (ledger.ID{}) && t.Credit != p.t.Credit,
		t.Ledger != 0 && t.Ledger != p.t.Ledger, t.Code != 0 && t.Code != p.t.Code:
		return ledger.ResultPendingDifferent
	}
	held := int64(p.t.Amount)
	amount := held // a void releases all of it
	if t.Flags.PostPending && t.Amount != ledger.AmountMax {
		if t.Amount > p.t.Amount {
			return ledger.ResultExceedsPendingAmount
		}
		amount = int64(t.Amount)
	}
	if t.Flags.VoidPending && t.Amount != 0 && t.Amount != p.t.Amount {
		if t.Amount > p.t.Amount {
			return ledger.ResultExceedsPendingAmount
		}
		return ledger.ResultPendingDifferent
	}
	da, ca := b.accounts[p.t.Debit], b.accounts[p.t.Credit]
	if da == nil || ca == nil {
		return ledger.ResultAccountNotFound // not locked: can't happen, load locks them
	}
	posted := int64(0)
	state := "voided"
	if t.Flags.PostPending {
		posted, state = amount, "posted"
	}
	b.change(da, -held, posted, 0, 0)
	b.change(ca, 0, 0, -held, posted)
	prev, prevMoved := p.state, p.moved
	p.state, p.moved = state, true
	b.undo = append(b.undo, func() { p.state, p.moved = prev, prevMoved })
	t.Debit, t.Credit, t.Ledger, t.Code, t.Amount = p.t.Debit, p.t.Credit, p.t.Ledger, p.t.Code, uint64(amount)
	b.add(&xfer{t: t, isNew: true})
	return ledger.ResultOK
}

// move applies a plain or pending transfer.
func (b *batch) move(t ledger.Transfer) ledger.Result {
	if t.Debit == t.Credit || t.Ledger == 0 || t.Code == 0 {
		return ledger.ResultInvalid
	}
	da, ca := b.accounts[t.Debit], b.accounts[t.Credit]
	if da == nil || ca == nil {
		return ledger.ResultAccountNotFound
	}
	if da.Ledger != int64(t.Ledger) || ca.Ledger != int64(t.Ledger) {
		return ledger.ResultLedgerMismatch
	}
	amount := t.Amount
	if t.Flags.BalancingDebit {
		amount = min(amount, uint64(max(da.CreditsPosted-da.DebitsPosted-da.DebitsPending, 0)))
	}
	if amount > math.MaxInt64 {
		return ledger.ResultOverflow
	}
	a := int64(amount)
	if da.DebitsPosted > math.MaxInt64-da.DebitsPending-a || ca.CreditsPosted > math.MaxInt64-ca.CreditsPending-a {
		return ledger.ResultOverflow
	}
	if da.NoOverdraft && da.DebitsPosted+da.DebitsPending+a > da.CreditsPosted {
		return ledger.ResultExceedsCredits
	}
	x := &xfer{isNew: true}
	if t.Flags.Pending {
		b.change(da, a, 0, 0, 0)
		b.change(ca, 0, 0, a, 0)
		x.state = "pending"
	} else {
		b.change(da, 0, a, 0, 0)
		b.change(ca, 0, 0, 0, a)
	}
	t.Amount = amount
	x.t = t
	b.add(x)
	return ledger.ResultOK
}

func (b *batch) change(a *account, dpend, dpost, cpend, cpost int64) {
	prev := *a
	a.DebitsPending += dpend
	a.DebitsPosted += dpost
	a.CreditsPending += cpend
	a.CreditsPosted += cpost
	a.dirty = true
	b.undo = append(b.undo, func() { *a = prev })
}

func (b *batch) add(x *xfer) {
	b.transfers[x.t.ID] = x
	b.undo = append(b.undo, func() { delete(b.transfers, x.t.ID) })
}

func (b *batch) write(ctx context.Context) error {
	var rows []store.InsertTransfersParams
	var states []store.SetPendingStateParams
	for _, x := range b.transfers {
		var state *string
		if x.state != "" {
			state = &x.state
		}
		switch {
		case x.isNew:
			pending := pgtype.UUID{}
			if x.t.PendingID != (ledger.ID{}) {
				pending = uuid(x.t.PendingID)
			}
			rows = append(rows, store.InsertTransfersParams{ID: uuid(x.t.ID), Debit: uuid(x.t.Debit), Credit: uuid(x.t.Credit),
				Amount: int64(x.t.Amount), PendingID: pending, Ledger: int64(x.t.Ledger), Code: int32(x.t.Code),
				Flags: bits(x.t.Flags), PendingState: state})
		case x.moved:
			states = append(states, store.SetPendingStateParams{ID: uuid(x.t.ID), PendingState: state})
		}
	}
	var accs []store.UpdateAccountParams
	for _, a := range b.accounts {
		if a.dirty {
			accs = append(accs, store.UpdateAccountParams{ID: a.ID, DebitsPending: a.DebitsPending,
				DebitsPosted: a.DebitsPosted, CreditsPending: a.CreditsPending, CreditsPosted: a.CreditsPosted})
		}
	}
	if len(rows) > 0 {
		if _, err := b.q.InsertTransfers(ctx, rows); err != nil {
			return err
		}
	}
	var errs []error
	collect := func(_ int, err error) { errs = append(errs, err) }
	if len(states) > 0 {
		b.q.SetPendingState(ctx, states).Exec(collect)
	}
	if len(b.newFailed) > 0 {
		b.q.InsertFailed(ctx, b.newFailed).Exec(collect)
	}
	if len(accs) > 0 {
		b.q.UpdateAccount(ctx, accs).Exec(collect)
	}
	return errors.Join(errs...)
}
