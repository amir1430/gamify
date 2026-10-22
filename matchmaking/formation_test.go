package matchmaking_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/realtime"
)

func addQueue(t *testing.T, e env, c matchmaking.QueueConfig) {
	t.Helper()
	if _, err := e.s.Queue(c); err != nil {
		t.Fatal(err)
	}
}

func staked(e env, name string, teams ...matchmaking.Team) matchmaking.QueueConfig {
	return matchmaking.QueueConfig{Name: name, Teams: teams, Game: e.a.Games.PvPGame(name, 1000, 1<<40, nil),
		Stakes: map[ledger.Currency][]int64{"usd": {100}}}
}

func matchNow(t *testing.T, e env) {
	t.Helper()
	if err := e.s.MatchNow(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// found is the match of the user's match.found pushes.
func found(e env, u int64) (ms []matchmaking.Match) {
	for _, raw := range e.a.Pushes(realtime.UserChannel(u)) {
		var m struct {
			Type  string
			Match matchmaking.Match
		}
		if json.Unmarshal(raw, &m) == nil && m.Type == "match.found" {
			ms = append(ms, m.Match)
		}
	}
	return
}

func pushTypes(e env, u int64, typ string) (n int) {
	for _, raw := range e.a.Pushes(realtime.UserChannel(u)) {
		var m struct{ Type string }
		if json.Unmarshal(raw, &m) == nil && m.Type == typ {
			n++
		}
	}
	return
}

func matchOf(t *testing.T, e env, id int64) matchmaking.Match {
	t.Helper()
	m, err := e.s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func onlyMatch(t *testing.T, e env) matchmaking.Match {
	t.Helper()
	var ids []int64
	rows, err := e.a.Pool.Query(context.Background(), `SELECT id FROM mm_matches ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Fatalf("matches %v", ids)
	}
	return matchOf(t, e, ids[0])
}

func TestFormFree(t *testing.T) {
	e := setup(t)
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	if _, cerr := queue(e, alice, duel("a")); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := queue(e, bob, duel("b")); cerr != nil {
		t.Fatal(cerr)
	}
	matchNow(t, e)
	m := onlyMatch(t, e)
	if m.Status != "running" || m.Queue != "duel" || len(m.Players) != 2 || m.Players[0].Team == m.Players[1].Team ||
		m.Players[0].EntryID >= m.Players[1].EntryID || m.Deadline.Before(time.Now().Add(59*time.Minute)) {
		t.Fatalf("match %+v", m)
	}
	if _, err := e.s.Get(context.Background(), m.ID+100); !errors.Is(err, matchmaking.ErrNotFound) {
		t.Fatalf("missing match: %v", err)
	}
	for u, key := range map[int64]string{alice: "a", bob: "b"} {
		if f := found(e, u); len(f) != 1 || f[0].ID != m.ID || f[0].Status != "running" || len(f[0].Players) != 2 {
			t.Fatalf("user %d found %+v", u, f)
		}
		st := statusOf(t, e, u)
		if st.Ticket != nil || len(st.Matches) != 1 || st.Matches[0].ID != m.ID {
			t.Fatalf("status %+v", st)
		}
		if r, cerr := queue(e, u, duel(key)); cerr != nil || r.MatchID != m.ID || r.Ticket != nil || r.Removed != "" {
			t.Fatalf("replay %+v %v", r, cerr)
		}
	}
	// a second tick changes nothing
	matchNow(t, e)
	if f := found(e, alice); len(f) != 1 {
		t.Fatal("found pushed twice")
	}
}

func TestFormStaked(t *testing.T) {
	e := setup(t)
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	for _, u := range []int64{alice, bob} {
		e.a.Fund(u, "usd", 1000)
		if _, cerr := queue(e, u, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "k"}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	m := onlyMatch(t, e)
	if m.Status != "running" || m.Stake != 100 || m.Currency != "usd" || m.FeeBP != ledger.FeeBP {
		t.Fatalf("match %+v", m)
	}
	for _, u := range []int64{alice, bob} {
		if b := e.a.Balance(u, "usd"); b != 900 {
			t.Fatalf("user %d balance %d", u, b)
		}
	}
	e.a.ReconcileNow() // nothing left to do: holds once
	if b := e.a.Balance(alice, "usd"); b != 900 {
		t.Fatalf("balance after reconcile %d", b)
	}
}

func TestFormBroke(t *testing.T) {
	e := setup(t)
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	e.a.Fund(alice, "usd", 1000)
	e.a.Fund(bob, "usd", 50)
	ra, cerr := queue(e, alice, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "a"})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := queue(e, bob, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "b"}); cerr != nil {
		t.Fatal(cerr)
	}
	matchNow(t, e)
	m := onlyMatch(t, e)
	if m.Status != "cancelled" || m.Reason != "insufficient_funds" {
		t.Fatalf("match %+v", m)
	}
	if a, b := e.a.Balance(alice, "usd"), e.a.Balance(bob, "usd"); a != 1000 || b != 50 {
		t.Fatalf("balances %d %d", a, b)
	}
	if got := removedPush(e, bob); len(got) != 1 || got[0] != "insufficient_funds" {
		t.Fatalf("bob pushes %v", got)
	}
	if got := removedPush(e, alice); len(got) != 0 || len(found(e, alice)) != 0 {
		t.Fatalf("alice pushes %v", got)
	}
	st := statusOf(t, e, alice)
	if st.Ticket == nil || st.Ticket.ID == ra.Ticket.ID || !st.Ticket.QueuedAt.Equal(ra.Ticket.QueuedAt) || len(st.Matches) != 0 {
		t.Fatalf("alice status %+v (was %+v)", st, ra.Ticket)
	}
	if ticketPushes(e, alice) != 2 {
		t.Fatal("re-queued ticket not pushed")
	}
	// replays: alice's key returns the live ticket, bob's why his went
	if r, _ := queue(e, alice, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "a"}); r.Ticket == nil || r.Ticket.ID != st.Ticket.ID {
		t.Fatalf("alice replay %+v", r)
	}
	if r, _ := queue(e, bob, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "b"}); r.Removed != "insufficient_funds" || r.MatchID != 0 {
		t.Fatalf("bob replay %+v", r)
	}
	if statusOf(t, e, bob).Ticket != nil {
		t.Fatal("bob still queued")
	}
	// alice matches with a funded carol, the second hold of her ticket is fresh
	carol := e.a.User("carol")
	e.a.Transport.SetOnline(alice, bob, carol)
	e.a.Fund(carol, "usd", 1000)
	if _, cerr := queue(e, carol, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "c"}); cerr != nil {
		t.Fatal(cerr)
	}
	matchNow(t, e)
	if f := found(e, alice); len(f) != 1 || e.a.Balance(alice, "usd") != 900 || e.a.Balance(carol, "usd") != 900 {
		t.Fatalf("rematch %+v", f)
	}
}

func TestFormBrokePartyMember(t *testing.T) {
	e := setup(t)
	addQueue(t, e, staked(e, "2v2", matchmaking.Team{2, 2}, matchmaking.Team{2, 2}))
	alice, bob, carol, dave := e.a.User("alice"), e.a.User("bob"), e.a.User("carol"), e.a.User("dave")
	e.a.Transport.SetOnline(alice, bob, carol, dave)
	for _, u := range []int64{alice, carol, dave} {
		e.a.Fund(u, "usd", 1000)
	}
	pr, _ := apptest.Call[party.Party](e.a, alice, "party.create", map[string]any{"open": true})
	if _, cerr := e.a.Call(bob, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
		t.Fatal(cerr)
	}
	req := queueReq{Queue: "2v2", Currency: "usd", Stake: 100}
	for i, u := range []int64{alice, carol, dave} {
		req.IdemKey = string(rune('a' + i))
		if _, cerr := queue(e, u, req); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	if m := onlyMatch(t, e); m.Status != "cancelled" || m.Reason != "insufficient_funds" || len(m.Players) != 4 {
		t.Fatalf("match %+v", m)
	}
	for _, u := range []int64{alice, bob} {
		if got := removedPush(e, u); len(got) != 1 || got[0] != "insufficient_funds" {
			t.Fatalf("user %d pushes %v", u, got)
		}
		if statusOf(t, e, u).Ticket != nil {
			t.Fatalf("user %d still queued", u)
		}
	}
	for _, u := range []int64{alice, carol, dave} {
		if b := e.a.Balance(u, "usd"); b != 1000 {
			t.Fatalf("user %d balance %d", u, b)
		}
	}
	for _, u := range []int64{carol, dave} {
		if st := statusOf(t, e, u); st.Ticket == nil || len(st.Ticket.Users) != 1 || st.Ticket.Users[0] != u {
			t.Fatalf("user %d status %+v", u, st)
		}
	}
}

func TestOfflineDropped(t *testing.T) {
	e := setup(t)
	addQueue(t, e, matchmaking.QueueConfig{Name: "signup", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, AllowOffline: true})
	alice, carol := e.a.User("alice"), e.a.User("carol")
	e.a.Transport.SetOnline(alice)
	if _, cerr := queue(e, alice, duel("a")); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := queue(e, carol, duel("c")); cerr != nil {
		t.Fatal(cerr)
	}
	matchNow(t, e)
	if got := removedPush(e, carol); len(got) != 1 || got[0] != "offline" {
		t.Fatalf("carol pushes %v", got)
	}
	if r, _ := queue(e, carol, duel("c")); r.Removed != "offline" {
		t.Fatalf("carol replay %+v", r)
	}
	if st := statusOf(t, e, alice); st.Ticket == nil {
		t.Fatal("online alice dropped")
	}
	if _, cerr := e.a.Call(alice, "match.cancel", nil); cerr != nil {
		t.Fatal(cerr)
	}
	// AllowOffline: both offline, kept and matched
	e.a.Transport.SetOnline()
	for u, k := range map[int64]string{alice: "a2", carol: "c2"} {
		if _, cerr := queue(e, u, queueReq{Queue: "signup", IdemKey: k}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	if m := onlyMatch(t, e); m.Status != "running" || m.Queue != "signup" {
		t.Fatalf("match %+v", m)
	}
}

func TestExpired(t *testing.T) {
	e := setup(t)
	addQueue(t, e, matchmaking.QueueConfig{Name: "short", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, MaxWait: time.Second})
	addQueue(t, e, matchmaking.QueueConfig{Name: "long", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, MaxWait: matchmaking.Forever})
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	if _, cerr := queue(e, alice, queueReq{Queue: "short", IdemKey: "a"}); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := queue(e, bob, queueReq{Queue: "long", IdemKey: "b"}); cerr != nil {
		t.Fatal(cerr)
	}
	matchNow(t, e)
	if statusOf(t, e, alice).Ticket == nil {
		t.Fatal("expired early")
	}
	time.Sleep(1100 * time.Millisecond)
	matchNow(t, e)
	if got := removedPush(e, alice); len(got) != 1 || got[0] != "expired" {
		t.Fatalf("alice pushes %v", got)
	}
	if statusOf(t, e, alice).Ticket != nil {
		t.Fatal("alice still queued")
	}
	if statusOf(t, e, bob).Ticket == nil || len(removedPush(e, bob)) != 0 {
		t.Fatal("Forever ticket dropped")
	}
}

func TestConcurrentTicks(t *testing.T) {
	e := setup(t)
	var users []int64
	for i := range 20 {
		users = append(users, e.a.User(string(rune('A'+i))))
	}
	e.a.Transport.SetOnline(users...)
	for _, u := range users {
		if _, cerr := queue(e, u, duel("k")); cerr != nil {
			t.Fatal(cerr)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 2 {
		wg.Go(func() {
			for range 5 {
				errs <- e.s.MatchNow(context.Background())
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	var matches, running, players, distinct, tickets, perMatch2 int
	q := `SELECT (SELECT count(*) FROM mm_matches), (SELECT count(*) FROM mm_matches WHERE status = 'running'),
		(SELECT count(*) FROM mm_match_players), (SELECT count(DISTINCT user_id) FROM mm_match_players),
		(SELECT count(*) FROM mm_tickets),
		(SELECT count(*) FROM (SELECT match_id FROM mm_requests GROUP BY match_id HAVING count(*) = 2 AND match_id > 0) x)`
	if err := e.a.Pool.QueryRow(ctx, q).Scan(&matches, &running, &players, &distinct, &tickets, &perMatch2); err != nil {
		t.Fatal(err)
	}
	if matches != 10 || running != 10 || players != 20 || distinct != 20 || tickets != 0 || perMatch2 != 10 {
		t.Fatalf("matches %d running %d players %d distinct %d tickets %d requests-per-match %d",
			matches, running, players, distinct, tickets, perMatch2)
	}
	for _, u := range users {
		if f := found(e, u); len(f) != 1 {
			t.Fatalf("user %d found %d", u, len(f))
		}
	}
}

func TestReconcileForming(t *testing.T) {
	e := setup(t)
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	var mu sync.Mutex
	calls, failed := 0, false
	restore := matchmaking.SetHoldPot(func(l *ledger.Ledger, ctx context.Context, g ledger.Game, c ledger.Currency, en ledger.PotEntry) error {
		mu.Lock()
		calls++
		f := !failed
		failed = true
		mu.Unlock()
		if f {
			return errors.New("ledger down")
		}
		return l.HoldPot(ctx, g, c, en)
	})
	defer restore()
	for _, u := range []int64{alice, bob} {
		e.a.Fund(u, "usd", 1000)
		if _, cerr := queue(e, u, queueReq{Queue: "bet", Currency: "usd", Stake: 100, IdemKey: "k"}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if err := e.s.MatchNow(context.Background()); err == nil {
		t.Fatal("transient error not reported")
	}
	if m := onlyMatch(t, e); m.Status != "forming" || len(found(e, alice)) != 0 {
		t.Fatalf("match %+v", m)
	}
	e.a.ReconcileNow()
	m := onlyMatch(t, e)
	if m.Status != "running" || len(found(e, alice)) != 1 || len(found(e, bob)) != 1 {
		t.Fatalf("after reconcile %+v", m)
	}
	for _, u := range []int64{alice, bob} {
		if b := e.a.Balance(u, "usd"); b != 900 {
			t.Fatalf("user %d balance %d", u, b)
		}
	}
	e.a.ReconcileNow()
	if b := e.a.Balance(alice, "usd"); b != 900 || len(found(e, alice)) != 1 {
		t.Fatal("second reconcile moved something")
	}
}

func TestOnMatchError(t *testing.T) {
	e := setup(t)
	var calls int
	var mu sync.Mutex
	c := staked(e, "hooked", matchmaking.Team{1, 1}, matchmaking.Team{1, 1})
	c.OnMatch = func(_ context.Context, m matchmaking.Match) error {
		mu.Lock()
		calls++
		mu.Unlock()
		if m.Status != "forming" || len(m.Players) != 2 {
			t.Errorf("OnMatch got %+v", m)
		}
		return errors.New("no server")
	}
	addQueue(t, e, c)
	alice, bob := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(alice, bob)
	for _, u := range []int64{alice, bob} {
		e.a.Fund(u, "usd", 1000)
		if _, cerr := queue(e, u, queueReq{Queue: "hooked", Currency: "usd", Stake: 100, IdemKey: "k"}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	m := onlyMatch(t, e)
	if m.Status != "cancelled" || m.Reason != "on_match" || calls != 1 {
		t.Fatalf("match %+v calls %d", m, calls)
	}
	for _, u := range []int64{alice, bob} {
		if b := e.a.Balance(u, "usd"); b != 1000 {
			t.Fatalf("user %d balance %d", u, b)
		}
		if len(found(e, u)) != 0 || pushTypes(e, u, "match.cancelled") != 1 {
			t.Fatalf("user %d pushes", u)
		}
		if st := statusOf(t, e, u); st.Ticket != nil || len(st.Matches) != 0 {
			t.Fatalf("status %+v", st)
		}
	}
	e.a.ReconcileNow()
	if calls != 1 {
		t.Fatal("OnMatch ran for a cancelled match")
	}
}
