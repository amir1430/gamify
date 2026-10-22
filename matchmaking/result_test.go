package matchmaking_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
)

// start funds users with 1000 usd, queues them with req and returns their running match.
func start(t *testing.T, e env, req queueReq, users ...int64) matchmaking.Match {
	t.Helper()
	e.a.Transport.SetOnline(users...)
	for _, u := range users {
		e.a.Fund(u, "usd", 1000)
		if _, cerr := queue(e, u, req); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	m := onlyMatch(t, e)
	if m.Status != "running" || len(m.Players) != len(users) {
		t.Fatalf("match %+v", m)
	}
	return m
}

func stakedReq(q string, stake int64) queueReq {
	return queueReq{Queue: q, Currency: "usd", Stake: stake, IdemKey: "k"}
}

func teamOf(m matchmaking.Match, u int64) int {
	return m.Players[slices.IndexFunc(m.Players, func(p matchmaking.Player) bool { return p.UserID == u })].Team
}

// wins ranks the winners' teams 1 and every other team 2.
func wins(m matchmaking.Match, winners ...int64) matchmaking.Result {
	n := 0
	for _, p := range m.Players {
		n = max(n, p.Team+1)
	}
	r := matchmaking.Result{Ranks: slices.Repeat([]int{2}, n)}
	for _, u := range winners {
		r.Ranks[teamOf(m, u)] = 1
	}
	return r
}

func balances(t *testing.T, e env, want map[int64]int64) {
	t.Helper()
	for u, w := range want {
		if b := e.a.Balance(u, "usd"); b != w {
			t.Fatalf("user %d balance %d, want %d", u, b, w)
		}
	}
}

type finished struct {
	Type    string
	Match   matchmaking.Match
	Payouts []struct {
		UserID int64 `json:"user_id"`
		Amount int64
	}
	Ratings map[string]struct{ Mu, Sigma, Ordinal float64 }
	Reason  string
}

func pushesOf(e env, u int64, typ string) (out []finished) {
	for _, raw := range e.a.Pushes(realtime.UserChannel(u)) {
		var f finished
		if json.Unmarshal(raw, &f) == nil && f.Type == typ {
			out = append(out, f)
		}
	}
	return
}

func TestFinishStaked1v1(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	if err := e.s.Finish(context.Background(), m.ID, wins(m, b)); err != nil {
		t.Fatal(err)
	}
	balances(t, e, map[int64]int64{a: 900, b: 1090})
	if m := matchOf(t, e, m.ID); m.Status != "settled" || m.Result == nil {
		t.Fatalf("match %+v", m)
	}
	for _, u := range []int64{a, b} {
		f := pushesOf(e, u, "match.finished")
		if len(f) != 1 || f[0].Match.Status != "settled" || len(f[0].Payouts) != 1 || f[0].Payouts[0].UserID != b ||
			f[0].Payouts[0].Amount != 190 || len(f[0].Ratings) != 0 {
			t.Fatalf("user %d pushes %+v", u, f)
		}
	}
}

func TestFinishFee(t *testing.T) {
	for _, c := range []struct {
		name  string
		fee   int64
		noFee bool
		want  int64
	}{{"fee", 1000, false, 1080}, {"nofee", 0, true, 1100}} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			q := staked(e, "fee", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
			q.FeeBP, q.NoFee = c.fee, c.noFee
			addQueue(t, e, q)
			a, b := e.a.User("alice"), e.a.User("bob")
			m := start(t, e, stakedReq("fee", 100), a, b)
			if err := e.s.Finish(context.Background(), m.ID, wins(m, b)); err != nil {
				t.Fatal(err)
			}
			balances(t, e, map[int64]int64{a: 900, b: c.want})
		})
	}
}

func TestFinishTeamsSplit(t *testing.T) {
	e := setup(t)
	q := staked(e, "2v2", matchmaking.Team{2, 2}, matchmaking.Team{2, 2})
	q.Stakes = map[ledger.Currency][]int64{"usd": {101}}
	addQueue(t, e, q)
	users := []int64{e.a.User("a"), e.a.User("b"), e.a.User("c"), e.a.User("d")}
	m := start(t, e, stakedReq("2v2", 101), users...)
	var won, lost []int64 // EntryID order
	for _, p := range m.Players {
		if p.Team == 0 {
			won = append(won, p.UserID)
		} else {
			lost = append(lost, p.UserID)
		}
	}
	if err := e.s.Finish(context.Background(), m.ID, matchmaking.Result{Ranks: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	// pot 404, fee 21, 383 = 192 + 191
	balances(t, e, map[int64]int64{won[0]: 899 + 192, won[1]: 899 + 191, lost[0]: 899, lost[1]: 899})
}

func TestFinishDraw(t *testing.T) {
	t.Run("fee", func(t *testing.T) {
		e := setup(t)
		a, b := e.a.User("alice"), e.a.User("bob")
		m := start(t, e, stakedReq("bet", 100), a, b)
		if err := e.s.Finish(context.Background(), m.ID, matchmaking.Result{Ranks: []int{1, 1}}); err != nil {
			t.Fatal(err)
		}
		balances(t, e, map[int64]int64{a: 995, b: 995})
	})
	t.Run("refund", func(t *testing.T) {
		e := setup(t)
		q := staked(e, "refund", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
		q.DrawRefund = true
		addQueue(t, e, q)
		a, b := e.a.User("alice"), e.a.User("bob")
		m := start(t, e, stakedReq("refund", 100), a, b)
		if err := e.s.Finish(context.Background(), m.ID, matchmaking.Result{Ranks: []int{1, 1}}); err != nil {
			t.Fatal(err)
		}
		balances(t, e, map[int64]int64{a: 1000, b: 1000})
		if m := matchOf(t, e, m.ID); m.Status != "settled" {
			t.Fatalf("match %+v", m)
		}
		if f := pushesOf(e, a, "match.finished"); len(f) != 1 || len(f[0].Payouts) != 0 {
			t.Fatalf("pushes %+v", f)
		}
	})
}

func TestFinishPayoutHook(t *testing.T) {
	e := setup(t)
	q := staked(e, "ffa3", matchmaking.Team{1, 1})
	q.FFA = &matchmaking.Team{3, 3}
	bad := true
	q.Payout = func(pot int64, m matchmaking.Match, r matchmaking.Result) []ledger.PotWin {
		var first, second int64
		for _, p := range m.Players {
			switch r.Ranks[p.Team] {
			case 1:
				first = p.UserID
			case 2:
				second = p.UserID
			}
		}
		left := pot - ledger.FeeOf(pot, m.FeeBP)
		top := left * 70 / 100
		if bad {
			return []ledger.PotWin{{UserID: first, Amount: top}, {UserID: second, Amount: left - top + 1}}
		}
		return []ledger.PotWin{{UserID: first, Amount: top}, {UserID: second, Amount: left - top}}
	}
	addQueue(t, e, q)
	a, b, c := e.a.User("a"), e.a.User("b"), e.a.User("c")
	m := start(t, e, stakedReq("ffa3", 100), a, b, c)
	r := matchmaking.Result{Ranks: make([]int, 3)}
	r.Ranks[teamOf(m, a)], r.Ranks[teamOf(m, b)], r.Ranks[teamOf(m, c)] = 1, 2, 3
	if err := e.s.Finish(context.Background(), m.ID, r); err == nil {
		t.Fatal("bad payout accepted")
	}
	if m := matchOf(t, e, m.ID); m.Status != "running" || m.Result != nil { // refused before storing
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 900, b: 900, c: 900})
	bad = false
	if err := e.s.Finish(context.Background(), m.ID, r); err != nil {
		t.Fatal(err)
	}
	// pot 300, fee 15, 285: 199 + 86
	balances(t, e, map[int64]int64{a: 1099, b: 986, c: 900})
	if m := matchOf(t, e, m.ID); m.Status != "settled" {
		t.Fatalf("match %+v", m)
	}
}

func TestFinishBadRanks(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	for _, r := range [][]int{{1}, {1, 2, 3}, {0, 1}} {
		if err := e.s.Finish(context.Background(), m.ID, matchmaking.Result{Ranks: r}); err == nil {
			t.Fatalf("ranks %v accepted", r)
		}
	}
	if m := matchOf(t, e, m.ID); m.Status != "running" {
		t.Fatalf("match %+v", m)
	}
}

func TestFinishTwice(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	ctx := context.Background()
	for range 2 {
		if err := e.s.Finish(ctx, m.ID, wins(m, b)); err != nil {
			t.Fatal(err)
		}
	}
	balances(t, e, map[int64]int64{a: 900, b: 1090})
	if err := e.s.Finish(ctx, m.ID, wins(m, a)); !errors.Is(err, matchmaking.ErrResultConflict) {
		t.Fatalf("different result: %v", err)
	}
	if err := e.s.Cancel(ctx, m.ID, "admin"); !errors.Is(err, matchmaking.ErrClosed) {
		t.Fatalf("cancel settled: %v", err)
	}
	e.a.ReconcileNow()
	balances(t, e, map[int64]int64{a: 900, b: 1090})
	if n := len(pushesOf(e, a, "match.finished")); n != 1 {
		t.Fatalf("finished pushed %d times", n)
	}
}

func TestFinishRanked(t *testing.T) {
	e := setup(t)
	var finishes []matchmaking.Match
	addQueue(t, e, matchmaking.QueueConfig{Name: "ranked", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Ranked: true,
		OnFinish: func(_ context.Context, m matchmaking.Match) error {
			finishes = append(finishes, m)
			return nil
		}})
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, queueReq{Queue: "ranked", IdemKey: "k"}, a, b)
	if err := e.s.Finish(context.Background(), m.ID, wins(m, a)); err != nil {
		t.Fatal(err)
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	for u, mu := range map[int64]float64{a: 27.63523138347365, b: 22.36476861652635} {
		r, err := e.s.Rating(context.Background(), u, "ranked")
		if err != nil || !near(r.Mu, mu) || !near(r.Sigma, 8.065506316323548) {
			t.Fatalf("user %d rating %+v %v", u, r, err)
		}
		got, cerr := apptest.Call[struct{ Matches int }](e.a, u, "rating.get", map[string]string{"key": "ranked"})
		if cerr != nil || got.Matches != 1 {
			t.Fatalf("user %d rating.get %+v %v", u, got, cerr)
		}
		f := pushesOf(e, u, "match.finished")
		pr := f[0].Ratings[strconv.FormatInt(u, 10)]
		if len(f) != 1 || len(f[0].Ratings) != 2 || !near(pr.Mu, mu) || !near(pr.Ordinal, mu-3*pr.Sigma) {
			t.Fatalf("user %d push %+v", u, f)
		}
	}
	if len(finishes) != 1 || finishes[0].Status != "settled" || finishes[0].Result == nil {
		t.Fatalf("OnFinish %+v", finishes)
	}
}

func TestCancelRefunds(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	ctx := context.Background()
	if err := e.s.Cancel(ctx, m.ID, ""); err == nil {
		t.Fatal("empty reason accepted")
	}
	if err := e.s.Cancel(ctx, m.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	balances(t, e, map[int64]int64{a: 1000, b: 1000})
	if m := matchOf(t, e, m.ID); m.Status != "cancelled" || m.Reason != "admin" {
		t.Fatalf("match %+v", m)
	}
	for _, u := range []int64{a, b} {
		if f := pushesOf(e, u, "match.cancelled"); len(f) != 1 || f[0].Reason != "admin" || f[0].Match.ID != m.ID {
			t.Fatalf("user %d pushes %+v", u, f)
		}
	}
	if err := e.s.Finish(ctx, m.ID, wins(m, b)); !errors.Is(err, matchmaking.ErrClosed) {
		t.Fatalf("finish after cancel: %v", err)
	}
	if err := e.s.Cancel(ctx, m.ID, "admin"); err != nil {
		t.Fatalf("cancel twice: %v", err)
	}
	if n := len(pushesOf(e, a, "match.cancelled")); n != 1 {
		t.Fatalf("cancelled pushed %d times", n)
	}
}

func TestTimeout(t *testing.T) {
	e := setup(t)
	q := staked(e, "short", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	q.MatchTimeout = time.Second
	addQueue(t, e, q)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("short", 100), a, b)
	e.a.ReconcileNow()
	if m := matchOf(t, e, m.ID); m.Status != "running" {
		t.Fatalf("cancelled early: %+v", m)
	}
	time.Sleep(1100 * time.Millisecond)
	e.a.ReconcileNow()
	if m := matchOf(t, e, m.ID); m.Status != "cancelled" || m.Reason != "timeout" {
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 1000, b: 1000})
}

func inTx(t *testing.T, e env, f func(tx pgx.Tx) error) {
	t.Helper()
	if err := pgx.BeginFunc(context.Background(), e.a.Pool, f); err != nil {
		t.Fatal(err)
	}
}

func TestReportRowOnly(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	ctx := context.Background()
	inTx(t, e, func(tx pgx.Tx) error { return e.s.Report(ctx, tx, m.ID, wins(m, b)) })
	if m := matchOf(t, e, m.ID); m.Status != "finishing" {
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 900, b: 900})
	e.a.ReconcileNow()
	if m := matchOf(t, e, m.ID); m.Status != "settled" {
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 900, b: 1090})
}

func TestMarkCancelRowOnly(t *testing.T) {
	e := setup(t)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	ctx := context.Background()
	inTx(t, e, func(tx pgx.Tx) error { return e.s.MarkCancel(ctx, tx, m.ID, "dispute") })
	if m := matchOf(t, e, m.ID); m.Status != "cancelling" {
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 900, b: 900})
	e.a.ReconcileNow()
	if m := matchOf(t, e, m.ID); m.Status != "cancelled" || m.Reason != "dispute" {
		t.Fatalf("match %+v", m)
	}
	balances(t, e, map[int64]int64{a: 1000, b: 1000})
}

func TestFinishNoWinners(t *testing.T) {
	e := setup(t)
	q := staked(e, "allfee", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	q.Stakes, q.FeeBP = map[ledger.Currency][]int64{"usd": {1}}, 9999 // pot 2, fee 2
	addQueue(t, e, q)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("allfee", 1), a, b)
	if err := e.s.Finish(context.Background(), m.ID, wins(m, a)); err == nil {
		t.Fatal("zero winners accepted")
	}
	if m := matchOf(t, e, m.ID); m.Status != "running" {
		t.Fatalf("match %+v", m)
	}
	if err := e.s.Cancel(context.Background(), m.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	balances(t, e, map[int64]int64{a: 1000, b: 1000})
}

func onFinishDone(t *testing.T, e env, id int64) (done bool) {
	t.Helper()
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT on_finish_done FROM mm_matches WHERE id = $1`, id).Scan(&done); err != nil {
		t.Fatal(err)
	}
	return
}

func TestOnFinishRetried(t *testing.T) {
	e := setup(t)
	calls, fail := 0, true
	q := staked(e, "hooked", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	q.Ranked = true
	q.OnFinish = func(_ context.Context, m matchmaking.Match) error {
		calls++
		if m.Status != "settled" {
			t.Errorf("OnFinish got %+v", m)
		}
		if fail {
			return errors.New("bracket down")
		}
		return nil
	}
	addQueue(t, e, q)
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("hooked", 100), a, b)
	_ = e.s.Finish(context.Background(), m.ID, wins(m, b))
	if m := matchOf(t, e, m.ID); m.Status != "settled" || onFinishDone(t, e, m.ID) || calls != 1 {
		t.Fatalf("match %+v calls %d", m, calls)
	}
	balances(t, e, map[int64]int64{a: 900, b: 1090})
	if r, _ := e.s.Rating(context.Background(), b, "hooked"); r.Mu <= 25 {
		t.Fatalf("rating %+v", r)
	}
	if n := len(pushesOf(e, a, "match.finished")); n != 1 {
		t.Fatalf("finished pushed %d times", n)
	}
	fail = false
	e.a.ReconcileNow()
	if calls != 2 || !onFinishDone(t, e, m.ID) {
		t.Fatalf("calls %d", calls)
	}
	e.a.ReconcileNow()
	if calls != 2 || len(pushesOf(e, a, "match.finished")) != 1 {
		t.Fatalf("calls %d after done", calls)
	}
	// a queue without OnFinish is done at once
	m2 := start2(t, e, "bet", e.a.User("carol"), e.a.User("dave"))
	if err := e.s.Finish(context.Background(), m2.ID, matchmaking.Result{Ranks: []int{1, 2}}); err != nil || !onFinishDone(t, e, m2.ID) {
		t.Fatalf("no hook: %v", err)
	}
}

// start2 is start for a test that already has matches: it returns the newest one.
func start2(t *testing.T, e env, q string, users ...int64) matchmaking.Match {
	t.Helper()
	e.a.Transport.SetOnline(users...)
	for _, u := range users {
		e.a.Fund(u, "usd", 1000)
		if _, cerr := queue(e, u, queueReq{Queue: q, Currency: "usd", Stake: 100, IdemKey: q}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	var id int64
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT max(id) FROM mm_matches`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	m := matchOf(t, e, id)
	if m.Status != "running" {
		t.Fatalf("match %+v", m)
	}
	return m
}

func TestOnFinishFinishesOther(t *testing.T) {
	e := setup(t)
	var second int64
	one := staked(e, "one", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	one.Ranked, one.RatingKey = true, "shared"
	one.OnFinish = func(ctx context.Context, m matchmaking.Match) error {
		return e.s.Finish(ctx, second, matchmaking.Result{Ranks: []int{1, 2}})
	}
	two := staked(e, "two", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	two.Ranked, two.RatingKey = true, "shared"
	addQueue(t, e, one)
	addQueue(t, e, two)
	a, b, c := e.a.User("alice"), e.a.User("bob"), e.a.User("carol")
	m1 := start2(t, e, "one", a, b)
	m2 := start2(t, e, "two", a, c)
	if m2.ID == m1.ID {
		t.Fatal("one match")
	}
	second = m2.ID
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.s.Finish(ctx, m1.ID, wins(m1, a)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{m1.ID, m2.ID} {
		if m := matchOf(t, e, id); m.Status != "settled" || !onFinishDone(t, e, id) {
			t.Fatalf("match %+v", m)
		}
	}
	got, _ := apptest.Call[struct{ Matches int }](e.a, a, "rating.get", map[string]string{"key": "shared"})
	if got.Matches != 2 {
		t.Fatalf("alice matches %d", got.Matches)
	}
}

func TestPayoutStored(t *testing.T) {
	e := setup(t)
	q := staked(e, "hooked", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	share := int64(150) // of 190
	q.Payout = func(pot int64, m matchmaking.Match, r matchmaking.Result) []ledger.PotWin {
		left := pot - ledger.FeeOf(pot, m.FeeBP)
		return []ledger.PotWin{{UserID: m.Players[0].UserID, Amount: share}, {UserID: m.Players[1].UserID, Amount: left - share}}
	}
	addQueue(t, e, q)
	failed := false
	restore := matchmaking.SetSettlePot(func(l *ledger.Ledger, ctx context.Context, g ledger.Game, c ledger.Currency, potID, feeBP int64,
		entries []ledger.PotEntry, winners []ledger.PotWin) (int64, error) {
		if !failed {
			failed = true
			return 0, errors.New("ledger down")
		}
		return l.SettlePotFee(ctx, g, c, potID, feeBP, entries, winners)
	})
	defer restore()
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("hooked", 100), a, b)
	if err := e.s.Finish(context.Background(), m.ID, matchmaking.Result{Ranks: []int{1, 2}}); err == nil {
		t.Fatal("ledger error not returned")
	}
	if m := matchOf(t, e, m.ID); m.Status != "finishing" {
		t.Fatalf("match %+v", m)
	}
	share = 40 // a changed hook doesn't change a stored result's payouts
	e.a.ReconcileNow()
	p0, p1 := m.Players[0].UserID, m.Players[1].UserID
	balances(t, e, map[int64]int64{p0: 900 + 150, p1: 900 + 40})
	if m := matchOf(t, e, m.ID); m.Status != "settled" {
		t.Fatalf("match %+v", m)
	}
}

// Settling holds a match row's transaction while the Postgres ledger takes a second connection
// from the same pool: concurrent Finish calls must not starve it.
func TestFinishSmallPool(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", os.Getenv("TEST_DATABASE_URL")+"&pool_max_conns=4")
	e := setup(t)
	if n := e.a.Pool.Config().MaxConns; n != 4 {
		t.Fatalf("pool size %d", n)
	}
	q := directBet(t, e)
	ctx := context.Background()
	var ms []matchmaking.Match
	for i := range 20 {
		a, b := e.a.User("a"+strconv.Itoa(i)), e.a.User("b"+strconv.Itoa(i))
		e.a.Fund(a, "usd", 1000)
		e.a.Fund(b, "usd", 1000)
		m, err := e.s.Create(ctx, matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a}, {b}}, Currency: "usd", Stake: 100,
			Ref: "r" + strconv.Itoa(i)})
		if err != nil || m.Status != "running" {
			t.Fatalf("create %d: %+v %v", i, m, err)
		}
		ms = append(ms, m)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(ms))
	for _, m := range ms {
		wg.Go(func() { errs <- e.s.Finish(ctx, m.ID, wins(m, m.Players[0].UserID)) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range ms {
		if got := matchOf(t, e, m.ID); got.Status != "settled" {
			t.Fatalf("match %+v", got)
		}
	}
}
