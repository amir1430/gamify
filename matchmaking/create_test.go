package matchmaking_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"
)

func queueOf(t *testing.T, e env, name string) *matchmaking.Queue {
	t.Helper()
	q, err := e.s.Queue(matchmaking.QueueConfig{Name: name + "-direct", Teams: []matchmaking.Team{{1, 2}, {1, 2}}, Concurrent: name == "conc"})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCreate(t *testing.T) {
	e := setup(t)
	q := queueOf(t, e, "x")
	a, b, c := e.a.User("a"), e.a.User("b"), e.a.User("c") // nobody online
	m, err := e.s.Create(context.Background(), matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a, b}, {c}}, Ref: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "running" || m.Ref != "r1" || len(m.Players) != 3 || m.Players[2].Team != 1 {
		t.Fatalf("match %+v", m)
	}
	for _, u := range []int64{a, b, c} {
		if f := found(e, u); len(f) != 1 || f[0].ID != m.ID {
			t.Fatalf("user %d found %+v", u, f)
		}
	}
	var snap string
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT tickets::text FROM mm_matches WHERE id=$1`, m.ID).Scan(&snap); err != nil || snap != "[]" {
		t.Fatalf("tickets %q %v", snap, err)
	}
}

func TestCreateRef(t *testing.T) {
	e := setup(t)
	g := e.a.Games.PvPGame("direct", 1000, 1<<40, nil)
	q, err := e.s.Queue(matchmaking.QueueConfig{Name: "dbet", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Game: g,
		Stakes: map[ledger.Currency][]int64{"usd": {100}}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := e.a.User("a"), e.a.User("b")
	e.a.Fund(a, "usd", 1000)
	e.a.Fund(b, "usd", 1000)
	n := matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a}, {b}}, Currency: "usd", Stake: 100, Ref: "r"}
	m1, err := e.s.Create(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := e.s.Create(context.Background(), n)
	if err != nil || m2.ID != m1.ID || m2.Status != "running" {
		t.Fatalf("repeat %+v %v", m2, err)
	}
	if bal := e.a.Balance(a, "usd"); bal != 900 {
		t.Fatalf("balance %d", bal)
	}
	if len(found(e, a)) != 1 {
		t.Fatal("found pushed twice")
	}
	for _, ref := range []string{"", "ticket:1"} {
		n.Ref = ref
		if _, err := e.s.Create(context.Background(), n); !errors.Is(err, gameconfig.ErrInvalid) {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
}

func TestCreateBroke(t *testing.T) {
	e := setup(t)
	g := e.a.Games.PvPGame("direct", 1000, 1<<40, nil)
	q, err := e.s.Queue(matchmaking.QueueConfig{Name: "dbet", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Game: g,
		Stakes: map[ledger.Currency][]int64{"usd": {100}}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := e.a.User("a"), e.a.User("b")
	e.a.Fund(a, "usd", 1000)
	e.a.Fund(b, "usd", 50)
	n := matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a}, {b}}, Currency: "usd", Stake: 100, Ref: "r"}
	if _, err := e.s.Create(context.Background(), n); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("err %v", err)
	}
	m := onlyMatch(t, e)
	if m.Status != "cancelled" || m.Reason != "insufficient_funds" {
		t.Fatalf("match %+v", m)
	}
	if x, y := e.a.Balance(a, "usd"), e.a.Balance(b, "usd"); x != 1000 || y != 50 {
		t.Fatalf("balances %d %d", x, y)
	}
	if st := statusOf(t, e, a); st.Ticket != nil || len(st.Matches) != 0 {
		t.Fatalf("requeued %+v", st)
	}
	// a repeat answers the same, without re-running
	if m2, err := e.s.Create(context.Background(), n); !errors.Is(err, ledger.ErrInsufficientFunds) || m2.ID != m.ID || m2.Status != "cancelled" {
		t.Fatalf("repeat %+v %v", m2, err)
	}
	// stake 0 on a staked queue, and a stake on a free one
	n.Ref, n.Stake = "r2", 0
	if _, err := e.s.Create(context.Background(), n); !errors.Is(err, gameconfig.ErrInvalid) {
		t.Fatalf("stake 0: %v", err)
	}
}

func TestCreateBusy(t *testing.T) {
	e := setup(t)
	a, b, c := e.a.User("a"), e.a.User("b"), e.a.User("c")
	for name, ok := range map[string]bool{"x": false, "conc": true} {
		q := queueOf(t, e, name)
		if _, err := e.s.Create(context.Background(), matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a}, {b}}, Ref: "1"}); err != nil {
			t.Fatal(err)
		}
		_, err := e.s.Create(context.Background(), matchmaking.NewMatch{Queue: q, Teams: [][]int64{{a}, {c}}, Ref: "2"})
		if ok != (err == nil) || !ok && !errors.Is(err, matchmaking.ErrBusy) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestCreateBadTeams(t *testing.T) {
	e := setup(t)
	q := queueOf(t, e, "x")
	ffa, err := e.s.Queue(matchmaking.QueueConfig{Name: "dffa", Teams: []matchmaking.Team{{1, 1}}, FFA: &matchmaking.Team{2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	u := make([]int64, 5)
	for i := range u {
		u[i] = e.a.User(string(rune('a' + i)))
	}
	for name, n := range map[string]matchmaking.NewMatch{
		"big team":        {Queue: q, Teams: [][]int64{{u[0], u[1], u[2]}, {u[3]}}, Ref: "1"},
		"empty team":      {Queue: q, Teams: [][]int64{{u[0]}, {}}, Ref: "2"},
		"one team":        {Queue: q, Teams: [][]int64{{u[0]}}, Ref: "3"},
		"dup user":        {Queue: q, Teams: [][]int64{{u[0]}, {u[0]}}, Ref: "4"},
		"stake free":      {Queue: q, Teams: [][]int64{{u[0]}, {u[1]}}, Currency: "usd", Stake: 1, Ref: "5"},
		"ffa too few":     {Queue: ffa, Teams: [][]int64{{u[0]}}, Ref: "6"},
		"ffa too many":    {Queue: ffa, Teams: [][]int64{{u[0]}, {u[1]}, {u[2]}, {u[3]}}, Ref: "7"},
		"ffa 2 in a team": {Queue: ffa, Teams: [][]int64{{u[0], u[1]}, {u[2]}}, Ref: "8"},
	} {
		if _, err := e.s.Create(context.Background(), n); !errors.Is(err, gameconfig.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := e.s.Create(context.Background(), matchmaking.NewMatch{Queue: ffa, Teams: [][]int64{{u[0]}, {u[1]}, {u[2]}}, Ref: "ok"}); err != nil {
		t.Fatal(err)
	}
}

func openMatches(t *testing.T, e env, u int64) (n int) {
	t.Helper()
	err := e.a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM mm_match_players p JOIN mm_matches m ON m.id=p.match_id
		WHERE p.user_id=$1 AND m.status NOT IN ('settled','cancelled')`, u).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func directBet(t *testing.T, e env) *matchmaking.Queue {
	t.Helper()
	q, err := e.s.Queue(matchmaking.QueueConfig{Name: "dbet2", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, AllowOffline: true,
		Game: e.a.Games.PvPGame("direct2", 1000, 1<<40, nil), Stakes: map[ledger.Currency][]int64{"usd": {100}}})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestCreateVsTicket(t *testing.T) {
	e := setup(t)
	q := directBet(t, e)
	u, v, w := e.a.User("u"), e.a.User("v"), e.a.User("w")
	for _, x := range []int64{u, v, w} {
		e.a.Fund(x, "usd", 1000)
	}
	// u and v queue; Create takes u first
	for i, x := range []int64{u, v} {
		if _, cerr := queue(e, x, queueReq{Queue: "dbet2", Currency: "usd", Stake: 100, IdemKey: string(rune('a' + i))}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if _, err := e.s.Create(context.Background(), matchmaking.NewMatch{Queue: q, Teams: [][]int64{{u}, {w}}, Currency: "usd", Stake: 100, Ref: "r"}); err != nil {
		t.Fatal(err)
	}
	matchNow(t, e)
	if n := openMatches(t, e, u); n != 1 {
		t.Fatalf("u in %d open matches", n)
	}
	if b := e.a.Balance(u, "usd"); b != 900 {
		t.Fatalf("u balance %d", b)
	}
}

func TestCreateConcurrentBusy(t *testing.T) {
	e := setup(t)
	q := directBet(t, e)
	u, v, w := e.a.User("u"), e.a.User("v"), e.a.User("w")
	for _, x := range []int64{u, v, w} {
		e.a.Fund(x, "usd", 1000)
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, o := range []int64{v, w} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.s.Create(context.Background(), matchmaking.NewMatch{Queue: q, Teams: [][]int64{{u}, {o}}, Currency: "usd", Stake: 100, Ref: string(rune('a' + i))})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, matchmaking.ErrBusy) {
			t.Fatal(err)
		}
	}
	if ok != 1 || openMatches(t, e, u) != 1 || e.a.Balance(u, "usd") != 900 {
		t.Fatalf("errs %v open %d balance %d", errs, openMatches(t, e, u), e.a.Balance(u, "usd"))
	}
}
