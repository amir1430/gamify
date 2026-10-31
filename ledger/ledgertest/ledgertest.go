// Package ledgertest is the conformance suite every ledger.Driver must pass: TigerBeetle's
// semantics at the driver level, then the ledger's money flows on top of it. A driver's test calls
// Run with a fresh driver (one per call; IDs are random, so a shared database is fine).
package ledgertest

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/amir1430/gamify/ledger"
)

// Currencies are the suite's: usd (ledger 1, not transferable) and tickets (ledger 2, 1,000
// welcome tickets, transferable between users).
var (
	USD     = ledger.CurrencyConfig{Code: "usd", Ledger: 1, MinAmount: 1}
	Tickets = ledger.CurrencyConfig{Code: "tickets", Ledger: 2, MinAmount: 10, Welcome: 1000, Transferable: true}
)

const (
	instant ledger.Game = 1
	held    ledger.Game = 2
	extra   ledger.Game = 3
	duel    ledger.Game = 4
	lottery ledger.Game = 5
	predict ledger.Game = 6
	shop    ledger.Game = 7
	gift    ledger.Game = 8
	prize   ledger.Game = 9
)

// Run runs the whole suite on drivers from newDriver.
func Run(t *testing.T, newDriver func(t *testing.T) ledger.Driver) {
	t.Run("Driver", func(t *testing.T) { driverSuite(t, newDriver(t)) })
	t.Run("Ledger", func(t *testing.T) { ledgerSuite(t, newDriver(t)) })
	t.Run("Pots", func(t *testing.T) { potSuite(t, newDriver(t)) })
	t.Run("TransferReader", func(t *testing.T) { readerSuite(t, newDriver(t)) })
}

func rid() uint64 { return 1<<40 + rand.Uint64N(1<<40) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func want[T comparable](t *testing.T, what string, got, w T) {
	t.Helper()
	if got != w {
		t.Fatalf("%s: %v, want %v", what, got, w)
	}
}

// --- Driver contract ---

func driverSuite(t *testing.T, d ledger.Driver) {
	ctx := context.Background()
	const lg, code = 9, 1
	acc := func(noOverdraft bool) ledger.ID {
		id := ledger.ID{Hi: 9, Lo: rid()}
		must(t, d.CreateAccounts(ctx, []ledger.Account{{ID: id, Ledger: lg, Code: code, NoOverdraft: noOverdraft}}))
		return id
	}
	tid := func() ledger.ID { return ledger.ID{Hi: 1 << 50, Lo: rid()} }
	xfer := func(id, from, to ledger.ID, amount uint64, f ledger.Flags) ledger.Transfer {
		return ledger.Transfer{ID: id, Debit: from, Credit: to, Amount: amount, Ledger: lg, Code: code, Flags: f}
	}
	// results returns the result of each transfer, ok included.
	results := func(ts ...ledger.Transfer) []ledger.Result {
		t.Helper()
		res, err := d.CreateTransfers(ctx, ts)
		must(t, err)
		out := make([]ledger.Result, len(ts))
		for i := range out {
			out[i] = ledger.ResultOK
		}
		for _, r := range res {
			out[r.Index] = r.Result
		}
		return out
	}
	expect := func(got []ledger.Result, w ...ledger.Result) {
		t.Helper()
		if !slices.Equal(got, w) {
			t.Fatalf("results %v, want %v", got, w)
		}
	}
	bal := func(id ledger.ID) ledger.AccountBalance {
		t.Helper()
		a, err := d.LookupAccounts(ctx, []ledger.ID{id})
		must(t, err)
		if len(a) != 1 {
			t.Fatalf("account %v: %d found", id, len(a))
		}
		return a[0]
	}
	spendable := func(id ledger.ID) int64 { b := bal(id); return b.CreditsPosted - b.DebitsPosted - b.DebitsPending }

	t.Run("accounts", func(t *testing.T) {
		id := acc(true)
		must(t, d.CreateAccounts(ctx, []ledger.Account{{ID: id, Ledger: lg, Code: code, NoOverdraft: true}}))
		if err := d.CreateAccounts(ctx, []ledger.Account{{ID: id, Ledger: lg, Code: 2, NoOverdraft: true}}); err == nil {
			t.Fatal("account recreated with a different code")
		}
		got, err := d.LookupAccounts(ctx, []ledger.ID{id, {Hi: 9, Lo: rid()}})
		must(t, err)
		want(t, "found", len(got), 1)
		want(t, "id", got[0].ID, id)
		want(t, "ledger", got[0].Ledger, uint32(lg))
	})

	t.Run("transfer, retry, conflict", func(t *testing.T) {
		src, dst := acc(false), acc(true)
		x := xfer(tid(), src, dst, 100, ledger.Flags{})
		expect(results(x), ledger.ResultOK)
		expect(results(x), ledger.ResultExists)
		x.Amount = 99
		expect(results(x), ledger.ResultExistsDifferent)
		want(t, "credited", spendable(dst), 100)
		want(t, "debited", spendable(src), -100)
		expect(results(xfer(tid(), src, ledger.ID{Hi: 9, Lo: rid()}, 1, ledger.Flags{})), ledger.ResultAccountNotFound)
	})

	t.Run("overdraft reserves the ID", func(t *testing.T) {
		src, user := acc(false), acc(true)
		expect(results(xfer(tid(), src, user, 50, ledger.Flags{})), ledger.ResultOK)
		x := xfer(tid(), user, src, 51, ledger.Flags{})
		expect(results(x), ledger.ResultExceedsCredits)
		expect(results(xfer(tid(), src, user, 10, ledger.Flags{})), ledger.ResultOK)
		expect(results(x), ledger.ResultIDAlreadyFailed) // refused for good, even with funds now
		want(t, "balance", spendable(user), 60)
	})

	t.Run("linked chain", func(t *testing.T) {
		src, user := acc(false), acc(true)
		expect(results(xfer(tid(), src, user, 100, ledger.Flags{})), ledger.ResultOK)
		a := xfer(tid(), user, src, 60, ledger.Flags{Linked: true})
		b := xfer(tid(), user, src, 60, ledger.Flags{})
		expect(results(a, b), ledger.ResultLinkedEventFailed, ledger.ResultExceedsCredits)
		want(t, "untouched", spendable(user), 100)
		// a was never applied nor reserved; b's ID is.
		expect(results(a, b), ledger.ResultLinkedEventFailed, ledger.ResultIDAlreadyFailed)
		a.Flags.Linked = false
		expect(results(a), ledger.ResultOK)

		// A full retry of an applied chain: exists, the rest linked_event_failed (applied once).
		c := xfer(tid(), src, user, 5, ledger.Flags{Linked: true})
		e := xfer(tid(), src, user, 6, ledger.Flags{})
		expect(results(c, e), ledger.ResultOK, ledger.ResultOK)
		expect(results(c, e), ledger.ResultExists, ledger.ResultLinkedEventFailed)
		want(t, "applied once", spendable(user), 51)

		// Independent transfers in one batch: each on its own.
		expect(results(xfer(tid(), user, src, 1000, ledger.Flags{}), xfer(tid(), user, src, 1, ledger.Flags{})),
			ledger.ResultExceedsCredits, ledger.ResultOK)
		// A chain left open at the end of the batch fails whole.
		expect(results(xfer(tid(), src, user, 1, ledger.Flags{Linked: true})), ledger.ResultLinkedChainOpen)
		want(t, "end", spendable(user), 50)
	})

	t.Run("pending, post, void", func(t *testing.T) {
		src, user, house := acc(false), acc(true), acc(false)
		expect(results(xfer(tid(), src, user, 100, ledger.Flags{})), ledger.ResultOK)
		hold := xfer(tid(), user, house, 70, ledger.Flags{Pending: true})
		expect(results(hold), ledger.ResultOK)
		want(t, "held", spendable(user), 30)
		want(t, "house pending", bal(house).CreditsPending, 70)
		expect(results(xfer(tid(), user, house, 31, ledger.Flags{})), ledger.ResultExceedsCredits)

		post := ledger.Transfer{ID: tid(), PendingID: hold.ID, Debit: user, Credit: house, Amount: 70, Ledger: lg, Code: code,
			Flags: ledger.Flags{PostPending: true}}
		expect(results(post), ledger.ResultOK)
		expect(results(post), ledger.ResultExists)
		post2 := post
		post2.ID = tid()
		expect(results(post2), ledger.ResultPendingAlreadyPosted)
		void := ledger.Transfer{ID: tid(), PendingID: hold.ID, Debit: user, Credit: house, Ledger: lg, Code: code,
			Flags: ledger.Flags{VoidPending: true}}
		expect(results(void), ledger.ResultPendingAlreadyPosted)
		want(t, "posted", spendable(user), 30)
		b := bal(house)
		want(t, "house posted", b.CreditsPosted, 70)
		want(t, "house pending after", b.CreditsPending, 0)

		hold2 := xfer(tid(), user, house, 20, ledger.Flags{Pending: true})
		expect(results(hold2), ledger.ResultOK)
		void.ID, void.PendingID = tid(), hold2.ID
		expect(results(void), ledger.ResultOK)
		want(t, "released", spendable(user), 30)
		want(t, "user pending", bal(user).DebitsPending, 0)

		missing := void
		missing.ID, missing.PendingID = tid(), tid()
		expect(results(missing), ledger.ResultPendingNotFound)
		expect(results(missing), ledger.ResultIDAlreadyFailed)

		// A post may move less than the hold; the rest goes back.
		hold3 := xfer(tid(), user, house, 30, ledger.Flags{Pending: true})
		expect(results(hold3), ledger.ResultOK)
		part := ledger.Transfer{ID: tid(), PendingID: hold3.ID, Debit: user, Credit: house, Amount: 31, Ledger: lg, Code: code,
			Flags: ledger.Flags{PostPending: true}}
		expect(results(part), ledger.ResultExceedsPendingAmount)
		part.ID, part.Amount = tid(), 10
		expect(results(part), ledger.ResultOK)
		want(t, "partial post", spendable(user), 20)

		// Hold and post in one linked batch.
		h := xfer(tid(), user, house, 5, ledger.Flags{Pending: true, Linked: true})
		p := ledger.Transfer{ID: tid(), PendingID: h.ID, Debit: user, Credit: house, Amount: 5, Ledger: lg, Code: code,
			Flags: ledger.Flags{PostPending: true}}
		expect(results(h, p), ledger.ResultOK, ledger.ResultOK)
		want(t, "same batch", spendable(user), 15)
	})

	t.Run("balancing debit", func(t *testing.T) {
		src, user, sink := acc(false), acc(true), acc(false)
		expect(results(xfer(tid(), src, user, 40, ledger.Flags{})), ledger.ResultOK)
		expect(results(xfer(tid(), user, sink, ledger.AmountMax, ledger.Flags{BalancingDebit: true})), ledger.ResultOK)
		want(t, "emptied", spendable(user), 0)
		want(t, "sink", bal(sink).CreditsPosted, 40)
		expect(results(xfer(tid(), user, sink, ledger.AmountMax, ledger.Flags{BalancingDebit: true})), ledger.ResultOK)
		want(t, "nothing left", bal(sink).CreditsPosted, 40)
	})
}

// --- TransferReader contract (optional) ---

func readerSuite(t *testing.T, d ledger.Driver) {
	r, ok := d.(ledger.TransferReader)
	if !ok {
		t.Skip("driver has no TransferReader")
	}
	ctx := context.Background()
	const lg = 9
	acc := func(noOverdraft bool) ledger.ID {
		id := ledger.ID{Hi: 9, Lo: rid()}
		must(t, d.CreateAccounts(ctx, []ledger.Account{{ID: id, Ledger: lg, Code: 1, NoOverdraft: noOverdraft}}))
		return id
	}
	tid := func() ledger.ID { return ledger.ID{Hi: 1 << 50, Lo: rid()} }
	apply := func(ts ...ledger.Transfer) {
		t.Helper()
		res, err := d.CreateTransfers(ctx, ts)
		must(t, err)
		if len(res) != 0 {
			t.Fatalf("results %v", res)
		}
	}
	get := func(f ledger.AccountFilter) []ledger.StoredTransfer {
		t.Helper()
		got, err := r.GetAccountTransfers(ctx, f)
		must(t, err)
		return got
	}
	ids := func(ts []ledger.StoredTransfer) []ledger.ID {
		out := make([]ledger.ID, len(ts))
		for i, x := range ts {
			out[i] = x.ID
		}
		return out
	}
	wantIDs := func(what string, got []ledger.StoredTransfer, w ...ledger.ID) {
		t.Helper()
		if !slices.Equal(ids(got), w) {
			t.Fatalf("%s: %v, want %v", what, ids(got), w)
		}
	}

	src, user, house := acc(false), acc(true), acc(false)
	t1 := ledger.Transfer{ID: tid(), Debit: src, Credit: user, Amount: 100, Ledger: lg, Code: 7}
	apply(t1)
	t2 := ledger.Transfer{ID: tid(), Debit: user, Credit: house, Amount: 30, Ledger: lg, Code: 1, Flags: ledger.Flags{Pending: true}}
	t3 := ledger.Transfer{ID: tid(), PendingID: t2.ID, Debit: user, Credit: house, Amount: 30, Ledger: lg, Code: 1, Flags: ledger.Flags{PostPending: true}}
	apply(t2, t3)
	t4 := ledger.Transfer{ID: tid(), Debit: user, Credit: house, Amount: 10, Ledger: lg, Code: 1, Flags: ledger.Flags{Pending: true}}
	apply(t4)
	t5 := ledger.Transfer{ID: tid(), PendingID: t4.ID, Debit: user, Credit: house, Ledger: lg, Code: 1, Flags: ledger.Flags{VoidPending: true}}
	apply(t5)
	// Refused transfers and broken chains leave nothing to read.
	if res, err := d.CreateTransfers(ctx, []ledger.Transfer{
		{ID: tid(), Debit: user, Credit: house, Amount: 1000, Ledger: lg, Code: 1},
		{ID: tid(), Debit: src, Credit: user, Amount: 1, Ledger: lg, Code: 1, Flags: ledger.Flags{Linked: true}},
		{ID: tid(), Debit: user, Credit: house, Amount: 1000, Ledger: lg, Code: 1},
	}); err != nil || len(res) != 3 {
		t.Fatalf("refusals: %v %v", res, err)
	}
	t6 := ledger.Transfer{ID: tid(), Debit: user, Credit: house, Amount: ledger.AmountMax, Ledger: lg, Code: 1, Flags: ledger.Flags{BalancingDebit: true}}
	apply(t6)

	both := ledger.AccountFilter{AccountID: user, Limit: 100, Debits: true, Credits: true}
	all := get(both)
	wantIDs("all", all, t1.ID, t2.ID, t3.ID, t4.ID, t5.ID, t6.ID)
	for i, x := range all {
		if x.Timestamp == 0 || (i > 0 && x.Timestamp <= all[i-1].Timestamp) {
			t.Fatalf("timestamps not increasing: %d after %d", x.Timestamp, all[max(i-1, 0)].Timestamp)
		}
	}
	// Stored as applied: the fields given, the amount that moved.
	want(t, "t1", all[0].Transfer, t1)
	want(t, "t3 pending id", all[2].PendingID, t2.ID)
	want(t, "t3 flags", all[2].Flags, ledger.Flags{PostPending: true})
	want(t, "t2 flags", all[1].Flags, ledger.Flags{Pending: true})
	want(t, "t5 void amount", all[4].Amount, uint64(10))
	want(t, "t6 balancing amount", all[5].Amount, uint64(70))
	want(t, "t6 debit", all[5].Debit, user)

	rev := both
	rev.Reversed = true
	wantIDs("reversed", get(rev), t6.ID, t5.ID, t4.ID, t3.ID, t2.ID, t1.ID)
	deb := both
	deb.Credits = false
	wantIDs("debits", get(deb), t2.ID, t3.ID, t4.ID, t5.ID, t6.ID)
	cred := both
	cred.Debits = false
	wantIDs("credits", get(cred), t1.ID)
	code := both
	code.Code = 7
	wantIDs("code", get(code), t1.ID)

	// Pages by timestamp, both ways.
	page := both
	page.Limit = 2
	p1 := get(page)
	wantIDs("page 1", p1, t1.ID, t2.ID)
	page.TimestampMin = p1[1].Timestamp + 1
	wantIDs("page 2", get(page), t3.ID, t4.ID)
	back := rev
	back.Limit = 2
	back.TimestampMax = all[3].Timestamp - 1
	wantIDs("back", get(back), t3.ID, t2.ID)
	span := both
	span.TimestampMin, span.TimestampMax = all[1].Timestamp, all[2].Timestamp
	wantIDs("span", get(span), t2.ID, t3.ID)

	// Filters that match nothing.
	wantIDs("house of none", get(ledger.AccountFilter{AccountID: ledger.ID{Hi: 9, Lo: rid()}, Limit: 10, Debits: true, Credits: true}))
	zero := both
	zero.Limit = 0
	wantIDs("limit 0", get(zero))
	none := both
	none.Debits, none.Credits = false, false
	wantIDs("no side", get(none))
	inverted := both
	inverted.TimestampMin, inverted.TimestampMax = all[2].Timestamp, all[1].Timestamp
	wantIDs("min above max", get(inverted))
}

// --- Ledger flows ---

type fixture struct {
	t *testing.T
	l *ledger.Ledger
}

func newFixture(t *testing.T, d ledger.Driver) fixture {
	l, err := ledger.New(d, USD, Tickets)
	must(t, err)
	must(t, l.Init(context.Background()))
	must(t, l.Init(context.Background())) // safe to repeat
	return fixture{t, l}
}

// user creates a user with $10.00 and the welcome tickets.
func (f fixture) user() int64 {
	id := int64(rid())
	must(f.t, f.l.EnsureUser(context.Background(), id))
	must(f.t, f.l.EnsureUser(context.Background(), id)) // the welcome grant is once
	must(f.t, f.l.TopUp(context.Background(), "usd", id, 1000))
	return id
}

func (f fixture) bal(id int64, c ledger.Currency) int64 {
	f.t.Helper()
	b, err := f.l.Balances(context.Background(), id)
	must(f.t, err)
	return b[c]
}

func ledgerSuite(t *testing.T, d ledger.Driver) {
	ctx := context.Background()
	f := newFixture(t, d)

	t.Run("welcome", func(t *testing.T) {
		u := f.user()
		want(t, "usd", f.bal(u, "usd"), 1000)
		want(t, "tickets", f.bal(u, "tickets"), 1000)
	})

	t.Run("instant", func(t *testing.T) {
		u := f.user()
		op := int64(rid())
		for range 2 { // retry moves nothing more
			must(t, f.l.SettleInstant(ctx, instant, "usd", op, u, 100, 198))
		}
		want(t, "won", f.bal(u, "usd"), 1098)
		must(t, f.l.SettleInstant(ctx, instant, "usd", int64(rid()), u, 98, 0))
		want(t, "lost", f.bal(u, "usd"), 1000)
		if err := f.l.SettleInstant(ctx, instant, "usd", int64(rid()), u, 1001, 2000); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("overdraft: %v", err)
		}
		want(t, "refused whole", f.bal(u, "usd"), 1000)
		must(t, f.l.SettleInstant(ctx, instant, "usd", int64(rid()), u, 0, 0)) // demo
		if err := f.l.SettleInstant(ctx, instant, "eur", int64(rid()), u, 1, 0); err == nil {
			t.Fatal("unknown currency accepted")
		}
	})

	t.Run("history", func(t *testing.T) {
		if !f.l.HasHistory() {
			t.Skip("driver has no TransferReader")
		}
		u := f.user() // welcome tickets, then a $10 top-up
		op := int64(rid())
		must(t, f.l.SettleInstant(ctx, instant, "usd", op, u, 300, 500))
		hop := int64(rid())
		must(t, f.l.Hold(ctx, held, "usd", hop, u, 200))
		must(t, f.l.SettleHeld(ctx, held, "usd", hop, u, 200, 0))
		must(t, f.l.Adjust(ctx, "usd", int64(rid()), u, -50))

		h, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 100})
		must(t, err)
		type row struct {
			kind, stage  string
			game         ledger.Game
			amount, post int64
			other        ledger.Party
		}
		var got []row
		for _, e := range h {
			got = append(got, row{e.Kind, e.Stage, e.Game, e.Amount, e.Posted, e.Other})
		}
		house := ledger.Party{System: "house"}
		w := []row{
			{"adjustment", "posted", 0, -50, -50, ledger.Party{System: "adjust"}},
			{"charge", "settled", held, -200, -200, house},
			{"charge", "held", held, -200, 0, house},
			{"payout", "posted", instant, 500, 500, house},
			{"charge", "posted", instant, -300, -300, house},
			{"top_up", "posted", 0, 1000, 1000, ledger.Party{System: "faucet"}},
		}
		if !slices.Equal(got, w) {
			t.Fatalf("history\n got %v\nwant %v", got, w)
		}
		// A running posted balance from today's back to zero.
		sum := int64(0)
		for _, e := range h {
			sum += e.Posted
		}
		want(t, "posted sum", sum, f.bal(u, "usd"))
		for i := 1; i < len(h); i++ {
			if !h[i].At.Before(h[i-1].At) && h[i].Timestamp >= h[i-1].Timestamp {
				t.Fatalf("not newest first at %d", i)
			}
		}

		// Pages, kinds, a time range.
		p1, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 2})
		must(t, err)
		p2, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 2, Before: p1[1].Timestamp})
		must(t, err)
		want(t, "page 2", p2[0].ID, h[2].ID)
		charges, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 100, Kind: "charge"})
		must(t, err)
		want(t, "charges", len(charges), 3)
		late, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 100, Since: h[1].At})
		must(t, err)
		want(t, "since", len(late) >= 2 && late[len(late)-1].At.Compare(h[1].At) >= 0, true)
		none, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 100, Until: h[len(h)-1].At})
		must(t, err)
		want(t, "until excludes", len(none), 0)
		if _, err := f.l.History(ctx, "usd", ledger.Party{User: u}, ledger.HistoryFilter{Limit: 1, Kind: "nope"}); err == nil {
			t.Fatal("unknown kind accepted")
		}

		// The house sees the same operation from its side.
		hh, err := f.l.History(ctx, "usd", house, ledger.HistoryFilter{Limit: 5, Since: h[len(h)-2].At})
		must(t, err)
		found := false
		for _, e := range hh {
			if e.Op == op && e.Kind == "payout" {
				found = e.Amount == -500 && e.Other == ledger.Party{User: u} && e.Game == instant
			}
		}
		want(t, "house payout", found, true)
		tot, err := f.l.Totals(ctx, "usd", ledger.Party{User: u})
		must(t, err)
		want(t, "totals", tot.CreditsPosted-tot.DebitsPosted, sum)
		if _, err := f.l.Totals(ctx, "usd", ledger.Party{System: "vault"}); err == nil {
			t.Fatal("unknown system account accepted")
		}
	})

	t.Run("held", func(t *testing.T) {
		u := f.user()
		op := int64(rid())
		must(t, f.l.Hold(ctx, held, "usd", op, u, 400))
		must(t, f.l.Hold(ctx, held, "usd", op, u, 400))
		want(t, "held", f.bal(u, "usd"), 600)
		for range 2 {
			must(t, f.l.SettleHeld(ctx, held, "usd", op, u, 400, 1000))
		}
		want(t, "cashed out", f.bal(u, "usd"), 1600)
		if err := f.l.Hold(ctx, held, "usd", int64(rid()), u, 1601); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("overdraft: %v", err)
		}
	})

	t.Run("extra holds", func(t *testing.T) {
		u := f.user()
		op := int64(rid())
		must(t, f.l.Hold(ctx, extra, "usd", op, u, 300))
		must(t, f.l.HoldExtra(ctx, extra, "usd", op, u, 1, 300))
		must(t, f.l.HoldExtra(ctx, extra, "usd", op, u, 1, 300)) // retry
		must(t, f.l.HoldExtra(ctx, extra, "usd", op, u, 3, 100)) // held, then unused: voided at settle
		if err := f.l.HoldExtra(ctx, extra, "usd", op, u, 2, 400); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("hold 2: %v", err)
		}
		want(t, "held", f.bal(u, "usd"), 300)
		for range 2 {
			must(t, f.l.SettleHolds(ctx, extra, "usd", op, u, 300, []int64{300}, 1200))
		}
		want(t, "settled", f.bal(u, "usd"), 1600) // 1000 - 600 + 1200, extra hold 3 back
	})

	t.Run("concurrent operations never overdraw", func(t *testing.T) {
		u := f.user()
		ops := make([]int64, 40)
		for i := range ops {
			ops[i] = int64(rid())
		}
		var wg sync.WaitGroup
		for i := range 2 * len(ops) { // every operation twice, at once: a retry racing its original
			wg.Go(func() {
				err := f.l.SettleInstant(ctx, instant, "usd", ops[i%len(ops)], u, 100, 0)
				// A retry of a refused operation finds its ID reserved: refused again, as an error.
				if err != nil && !errors.Is(err, ledger.ErrInsufficientFunds) && !strings.Contains(err.Error(), string(ledger.ResultIDAlreadyFailed)) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		want(t, "balance", f.bal(u, "usd"), 0) // ten ops of 100 took the $10, none twice, none past it
	})

	t.Run("deposit, reward, swap", func(t *testing.T) {
		u := f.user()
		dep := int64(rid())
		for range 2 {
			must(t, f.l.Deposit(ctx, "usd", dep, u, 500))
		}
		want(t, "deposited", f.bal(u, "usd"), 1500)
		paid0, err := f.l.RewardsPaid(ctx, "tickets")
		must(t, err)
		claim := int64(rid())
		for range 2 {
			must(t, f.l.Reward(ctx, "tickets", claim, u, 250))
		}
		want(t, "rewarded", f.bal(u, "tickets"), 1250)
		paid, err := f.l.RewardsPaid(ctx, "tickets")
		must(t, err)
		want(t, "rewards paid", paid-paid0, 250)

		// Buy 1,500 tickets for $10 with a 100 bonus.
		buy := int64(rid())
		for range 2 {
			must(t, f.l.Swap(ctx, buy, u, "usd", 1000, "tickets", 1500, 100, 0))
		}
		want(t, "usd after buy", f.bal(u, "usd"), 500)
		want(t, "tickets after buy", f.bal(u, "tickets"), 2850)
		// Selling into the welcome tickets is refused whole.
		if err := f.l.Swap(ctx, int64(rid()), u, "tickets", 1851, "usd", 1, 0, 0); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("sold welcome tickets: %v", err)
		}
		must(t, f.l.Swap(ctx, int64(rid()), u, "tickets", 850, "usd", 500, 0, 0))
		want(t, "tickets after sale", f.bal(u, "tickets"), 2000)
		// A forfeiting sale leaves exactly the welcome tickets.
		must(t, f.l.Swap(ctx, int64(rid()), u, "tickets", 100, "usd", 50, 0, 1))
		want(t, "forfeited", f.bal(u, "tickets"), 1000)
		want(t, "usd end", f.bal(u, "usd"), 1050)
	})

	t.Run("prize", func(t *testing.T) {
		u := f.user()
		paid0, err := f.l.RewardsPaid(ctx, "tickets")
		must(t, err)
		claim := int64(rid())
		for range 2 {
			must(t, f.l.RewardPrize(ctx, prize, "tickets", claim, u, 250))
		}
		want(t, "prized", f.bal(u, "tickets"), 1250)
		// The same ID through Reward is another transfer: the namespaces don't collide.
		must(t, f.l.Reward(ctx, "tickets", claim, u, 250))
		want(t, "both paid", f.bal(u, "tickets"), 1500)
		paid, err := f.l.RewardsPaid(ctx, "tickets")
		must(t, err)
		want(t, "rewards paid", paid-paid0, 500)
	})

	t.Run("adjust", func(t *testing.T) {
		u := f.user()
		sb0, err := f.l.SystemBalances(ctx, "usd")
		must(t, err)
		id := int64(rid())
		for range 2 { // retry: no double credit
			must(t, f.l.Adjust(ctx, "usd", id, u, 500))
		}
		want(t, "credited", f.bal(u, "usd"), 1500)
		must(t, f.l.Adjust(ctx, "usd", id+1, u, -200))
		want(t, "debited", f.bal(u, "usd"), 1300)
		if err := f.l.Adjust(ctx, "usd", id+2, u, -1301); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("overdraft: %v", err)
		}
		if err := f.l.Adjust(ctx, "usd", id+2, u, -1301); !errors.Is(err, ledger.ErrAlreadyFailed) {
			t.Fatalf("retried overdraft: %v", err)
		}
		want(t, "refused", f.bal(u, "usd"), 1300)
		sb, err := f.l.SystemBalances(ctx, "usd")
		must(t, err)
		want(t, "adjust account", sb["adjust"]-sb0["adjust"], -300)
		for _, k := range []string{"house", "pot", "exchange", "inflow", "faucet", "grant", "floor", "rewards", "adjust", "sales"} {
			if _, ok := sb[k]; !ok {
				t.Fatalf("system balances: no %q in %v", k, sb)
			}
		}
		if err := f.l.Adjust(ctx, "usd", id+3, u, 0); err == nil {
			t.Fatal("zero adjust accepted")
		}
		if err := f.l.Adjust(ctx, "eur", id+4, u, 1); err == nil {
			t.Fatal("unknown currency accepted")
		}
	})

	t.Run("spend", func(t *testing.T) {
		u := f.user()
		sb0, err := f.l.SystemBalances(ctx, "usd")
		must(t, err)
		op := int64(rid())
		for range 2 { // retry: charged once
			must(t, f.l.Spend(ctx, shop, "usd", op, u, 300))
		}
		want(t, "spent", f.bal(u, "usd"), 700)
		sb, err := f.l.SystemBalances(ctx, "usd")
		must(t, err)
		want(t, "sales account", sb["sales"]-sb0["sales"], 300)
		must(t, f.l.Spend(ctx, shop, "usd", int64(rid()), u, 0))
		want(t, "zero moves nothing", f.bal(u, "usd"), 700)
		refused := int64(rid())
		if err := f.l.Spend(ctx, shop, "usd", refused, u, 701); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("overdraft: %v", err)
		}
		must(t, f.l.TopUp(ctx, "usd", u, 1000))
		if err := f.l.Spend(ctx, shop, "usd", refused, u, 701); !errors.Is(err, ledger.ErrAlreadyFailed) {
			t.Fatalf("retried overdraft: %v", err)
		}
		if err := f.l.Spend(ctx, shop, "usd", int64(rid()), u, -1); err == nil {
			t.Fatal("negative spend accepted")
		}
		want(t, "refused", f.bal(u, "usd"), 1700)
		// Spending is play, not cash-out: the welcome tickets may be spent.
		v := f.user()
		must(t, f.l.Spend(ctx, shop, "tickets", int64(rid()), v, 1000))
		want(t, "welcome spent", f.bal(v, "tickets"), 0)
		if err := f.l.Spend(ctx, shop, "eur", int64(rid()), u, 1); err == nil {
			t.Fatal("unknown currency accepted")
		}
	})

	t.Run("transfer", func(t *testing.T) {
		a, b := f.user(), f.user()
		for _, amount := range []int64{10, 0} {
			if err := f.l.Transfer(ctx, gift, "usd", int64(rid()), a, b, amount); !errors.Is(err, ledger.ErrNotTransferable) {
				t.Fatalf("usd %d: %v", amount, err)
			}
		}
		// a buys 1,500 tickets: 2,500 in all, 1,000 of them the play-only welcome grant.
		must(t, f.l.Swap(ctx, int64(rid()), a, "usd", 1000, "tickets", 1500, 0, 0))
		op := int64(rid())
		for range 2 { // retry: sent once
			must(t, f.l.Transfer(ctx, gift, "tickets", op, a, b, 1500))
		}
		want(t, "sender", f.bal(a, "tickets"), 1000)
		want(t, "receiver", f.bal(b, "tickets"), 2500)
		if err := f.l.Transfer(ctx, gift, "tickets", int64(rid()), a, b, 1); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("into the welcome grant: %v", err)
		}
		if err := f.l.Transfer(ctx, gift, "tickets", int64(rid()), f.user(), b, 1); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("welcome only: %v", err)
		}
		if err := f.l.Transfer(ctx, gift, "tickets", int64(rid()), b, b, 1); err == nil {
			t.Fatal("transfer to self accepted")
		}
		if err := f.l.Transfer(ctx, gift, "tickets", int64(rid()), b, a, -1); err == nil {
			t.Fatal("negative transfer accepted")
		}
		must(t, f.l.Transfer(ctx, gift, "tickets", int64(rid()), b, a, 0))
		want(t, "sender after refusals", f.bal(a, "tickets"), 1000)
		want(t, "receiver after refusals", f.bal(b, "tickets"), 2500)
		if err := f.l.Transfer(ctx, gift, "eur", int64(rid()), a, b, 1); err == nil {
			t.Fatal("unknown currency accepted")
		}
	})

	t.Run("one operation ID, three moves", func(t *testing.T) {
		u, v := f.user(), f.user()
		op := int64(rid())
		must(t, f.l.SettleInstant(ctx, gift, "usd", op, u, 100, 0))
		must(t, f.l.Spend(ctx, gift, "usd", op, u, 100))
		want(t, "usd", f.bal(u, "usd"), 800)
		must(t, f.l.Swap(ctx, int64(rid()), v, "usd", 500, "tickets", 500, 0, 0))
		must(t, f.l.Transfer(ctx, gift, "tickets", op, v, u, 100))
		want(t, "tickets", f.bal(u, "tickets"), 1100)
	})
}

// --- Pots (PvP) ---

func potSuite(t *testing.T, d ledger.Driver) {
	ctx := context.Background()
	f := newFixture(t, d)
	entry := func(u, amount int64) ledger.PotEntry {
		return ledger.PotEntry{ID: int64(rid()), UserID: u, Amount: amount}
	}
	pot := func() int64 { return int64(rid()) }
	usd := func(u int64) int64 { return f.bal(u, "usd") }
	// house is the usd house account's posted balance (ID scheme: (0, ledger<<8 | 1)).
	house := func() int64 {
		a, err := d.LookupAccounts(ctx, []ledger.ID{{Hi: 0, Lo: uint64(USD.Ledger)<<8 | 1}})
		must(t, err)
		return a[0].CreditsPosted - a[0].DebitsPosted
	}

	t.Run("duel", func(t *testing.T) {
		a, b := f.user(), f.user()
		ea, eb := entry(a, 500), entry(b, 500)
		must(t, f.l.HoldPot(ctx, duel, "usd", ea))
		must(t, f.l.HoldPot(ctx, duel, "usd", eb))
		must(t, f.l.HoldPot(ctx, duel, "usd", eb)) // retry
		want(t, "A held", usd(a), 500)
		p := pot()
		for range 2 {
			fee, err := f.l.SettlePot(ctx, duel, "usd", p, []ledger.PotEntry{ea, eb}, []ledger.PotWin{{UserID: b, Amount: 950}})
			must(t, err)
			want(t, "fee", fee, 50)
			want(t, "A", usd(a), 500)
			want(t, "B", usd(b), 1450)
		}
	})

	t.Run("fee per pot", func(t *testing.T) {
		a, b := f.user(), f.user()
		ea, eb := entry(a, 500), entry(b, 500)
		must(t, f.l.HoldPot(ctx, duel, "usd", ea))
		must(t, f.l.HoldPot(ctx, duel, "usd", eb))
		p := pot()
		for range 2 {
			fee, err := f.l.SettlePotFee(ctx, duel, "usd", p, 0, []ledger.PotEntry{ea, eb}, []ledger.PotWin{{UserID: b, Amount: 1000}})
			must(t, err)
			want(t, "fee", fee, 0)
			want(t, "A", usd(a), 500)
			want(t, "B", usd(b), 1500)
		}
	})

	t.Run("fee per pot refuses bad bp", func(t *testing.T) {
		a, b := f.user(), f.user()
		ea, eb := entry(a, 500), entry(b, 500)
		must(t, f.l.HoldPot(ctx, duel, "usd", ea))
		must(t, f.l.HoldPot(ctx, duel, "usd", eb))
		for _, bp := range []int64{-1, 10_001} {
			if _, err := f.l.SettlePotFee(ctx, duel, "usd", pot(), bp, []ledger.PotEntry{ea, eb}, []ledger.PotWin{{UserID: b, Amount: 1000}}); err == nil {
				t.Fatalf("bp %d accepted", bp)
			}
			es := []ledger.PotShare{{EntryID: eb.ID, UserID: b, Amount: 1000}}
			if _, err := f.l.SettlePoolFee(ctx, duel, "usd", pot(), bp, []ledger.PotEntry{ea, eb}, es); err == nil {
				t.Fatalf("pool bp %d accepted", bp)
			}
		}
		want(t, "A", usd(a), 500)
		want(t, "B", usd(b), 500)
	})

	t.Run("pool fee", func(t *testing.T) {
		var es []ledger.PotEntry
		for range 3 {
			e := entry(f.user(), 100)
			must(t, f.l.HoldPot(ctx, predict, "usd", e))
			es = append(es, e)
		}
		share := func(n int64) []ledger.PotShare {
			return []ledger.PotShare{{EntryID: es[0].ID, UserID: es[0].UserID, Amount: n}}
		}
		if _, err := f.l.SettlePoolFee(ctx, predict, "usd", pot(), 1000, es, share(271)); err == nil {
			t.Fatal("271 accepted")
		}
		fee, err := f.l.SettlePoolFee(ctx, predict, "usd", pot(), 1000, es, share(270))
		must(t, err)
		want(t, "fee", fee, 30)
		want(t, "winner", usd(es[0].UserID), 900+270)
	})

	t.Run("lottery", func(t *testing.T) {
		a, b, c := f.user(), f.user(), f.user()
		es := []ledger.PotEntry{entry(a, 1000), entry(b, 1000), entry(c, 1000)}
		for _, e := range es {
			must(t, f.l.HoldPot(ctx, lottery, "usd", e))
		}
		p := pot()
		if _, err := f.l.SettlePot(ctx, lottery, "usd", p, es, []ledger.PotWin{{UserID: a, Amount: 1425}, {UserID: b, Amount: 855}, {UserID: a, Amount: 569}}); err == nil {
			t.Fatal("2849 accepted")
		}
		want(t, "A held", usd(a), 0)
		_, err := f.l.SettlePot(ctx, lottery, "usd", p, es, []ledger.PotWin{{UserID: a, Amount: 1425}, {UserID: b, Amount: 855}, {UserID: a, Amount: 570}})
		must(t, err)
		want(t, "A", usd(a), 1995)
		want(t, "B", usd(b), 855)
		want(t, "C", usd(c), 0)
	})

	t.Run("void", func(t *testing.T) {
		a := f.user()
		e := entry(a, 600)
		must(t, f.l.HoldPot(ctx, duel, "usd", e))
		want(t, "held", usd(a), 400)
		must(t, f.l.VoidPot(ctx, duel, "usd", []ledger.PotEntry{e}))
		must(t, f.l.VoidPot(ctx, duel, "usd", []ledger.PotEntry{e, entry(a, 5)})) // again, and one never held
		want(t, "back", usd(a), 1000)
	})

	t.Run("winner cap", func(t *testing.T) {
		a := f.user()
		e := entry(a, 1000)
		must(t, f.l.HoldPot(ctx, lottery, "usd", e))
		// 248 winners sharing pot - fee (950): one past the cap, otherwise a valid settle.
		winners := make([]ledger.PotWin, ledger.MaxPotWinners+1)
		for i := range winners {
			winners[i] = ledger.PotWin{UserID: a, Amount: 3}
		}
		winners[0].Amount = 950 - 3*int64(len(winners)-1)
		p := pot()
		if _, err := f.l.SettlePot(ctx, lottery, "usd", p, []ledger.PotEntry{e}, winners); !errors.Is(err, ledger.ErrTooManyWinners) {
			t.Fatalf("248 winners: %v", err)
		}
		want(t, "still held", usd(a), 0)
		must(t, f.l.VoidPot(ctx, lottery, "usd", []ledger.PotEntry{e}))
		want(t, "back", usd(a), 1000)
	})

	t.Run("welcome stays play-only", func(t *testing.T) {
		a := f.user()
		e := entry(a, 10)
		for range 2 {
			if err := f.l.HoldPot(ctx, duel, "tickets", e); !errors.Is(err, ledger.ErrInsufficientFunds) {
				t.Fatalf("welcome tickets: %v", err)
			}
		}
		want(t, "tickets", f.bal(a, "tickets"), 1000)
		a = f.user()
		must(t, f.l.Swap(ctx, int64(rid()), a, "usd", 1000, "tickets", 1500, 0, 0))
		must(t, f.l.HoldPot(ctx, duel, "tickets", entry(a, 1500)))
		if err := f.l.HoldPot(ctx, duel, "tickets", entry(a, 1)); !errors.Is(err, ledger.ErrInsufficientFunds) {
			t.Fatalf("one over: %v", err)
		}
		want(t, "left", f.bal(a, "tickets"), 1000)
	})

	pool := func() []ledger.PotEntry {
		var es []ledger.PotEntry
		for _, amount := range []int64{600, 200, 200, 100, 100} {
			e := entry(f.user(), amount)
			must(t, f.l.HoldPot(ctx, predict, "usd", e))
			es = append(es, e)
		}
		return es
	}

	t.Run("pool", func(t *testing.T) {
		es := pool()
		shares := []ledger.PotShare{{EntryID: es[1].ID, UserID: es[1].UserID, Amount: 400}, {EntryID: es[2].ID, UserID: es[2].UserID, Amount: 740}}
		h0, p := house(), pot()
		for range 2 {
			fee, err := f.l.SettlePool(ctx, predict, "usd", p, es, shares)
			must(t, err)
			want(t, "fee", fee, 60)
			want(t, "winner 1", usd(es[1].UserID), 800+400)
			want(t, "winner 2", usd(es[2].UserID), 800+740)
			want(t, "loser", usd(es[0].UserID), 400)
			want(t, "house", house()-h0, 60)
		}
	})

	t.Run("pool refusals", func(t *testing.T) {
		es := pool()
		good := ledger.PotShare{EntryID: es[1].ID, UserID: es[1].UserID, Amount: 1140}
		for name, shares := range map[string][]ledger.PotShare{
			"bad sum":   {{EntryID: es[1].ID, UserID: es[1].UserID, Amount: 1139}},
			"negative":  {{EntryID: es[1].ID, UserID: es[1].UserID, Amount: 1141}, {EntryID: es[2].ID, UserID: es[2].UserID, Amount: -1}},
			"duplicate": {{EntryID: es[1].ID, UserID: es[1].UserID, Amount: 570}, {EntryID: es[1].ID, UserID: es[1].UserID, Amount: 570}},
			"unknown":   {{EntryID: es[1].ID + 1, UserID: es[1].UserID, Amount: 1140}},
		} {
			if _, err := f.l.SettlePool(ctx, predict, "usd", pot(), es, shares); err == nil {
				t.Fatalf("%s accepted", name)
			}
		}
		want(t, "nothing paid", usd(es[1].UserID), 800)
		// A voided hold: its post fails, so neither the fee nor any share may apply.
		must(t, f.l.VoidPot(ctx, predict, "usd", es[4:]))
		h0 := house()
		if _, err := f.l.SettlePool(ctx, predict, "usd", pot(), es, []ledger.PotShare{good}); err == nil {
			t.Fatal("voided hold accepted")
		}
		want(t, "no share after a failed post", usd(es[1].UserID), 800)
		want(t, "no fee after a failed post", house(), h0)
	})

	t.Run("pool resumes", func(t *testing.T) {
		es := pool()
		shares := []ledger.PotShare{{EntryID: es[0].ID, UserID: es[0].UserID, Amount: 1140}}
		// The posts go through, then the call that pays fails (a crash between the two).
		crashing, err := ledger.New(&failNth{Driver: d, n: 2}, USD, Tickets)
		must(t, err)
		p := pot()
		if _, err := crashing.SettlePool(ctx, predict, "usd", p, es, shares); err == nil {
			t.Fatal("crash not seen")
		}
		want(t, "posted, not paid", usd(es[0].UserID), 400)
		_, err = f.l.SettlePool(ctx, predict, "usd", p, es, shares)
		must(t, err)
		want(t, "paid on resume", usd(es[0].UserID), 400+1140)
	})
}

// failNth fails the nth CreateTransfers call.
type failNth struct {
	ledger.Driver
	n, calls int
}

func (f *failNth) CreateTransfers(ctx context.Context, ts []ledger.Transfer) ([]ledger.TransferResult, error) {
	if f.calls++; f.calls == f.n {
		return nil, errors.New("crash")
	}
	return f.Driver.CreateTransfers(ctx, ts)
}
