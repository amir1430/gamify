package matchmaking_test

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/realtime"
)

type env struct {
	a *apptest.App
	p *party.Service
	s *matchmaking.Service
}

func setup(t *testing.T) env {
	t.Helper()
	return setupOn(t, apptest.New(t))
}

// setupOn registers parties, matchmaking and the test queues on a.
func setupOn(t *testing.T, a *apptest.App) env {
	t.Helper()
	ctx := context.Background()
	p, err := party.Register(ctx, a.App, party.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := matchmaking.Register(ctx, a.App, p, matchmaking.Config{})
	if err != nil {
		t.Fatal(err)
	}
	g := a.Games.PvPGame("arena", 1000, 1<<40, nil)
	for _, c := range []matchmaking.QueueConfig{
		{Name: "duel", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Properties: []string{"mode"},
			Numbers: map[string]matchmaking.NumberRule{"level": {Diff: 2, Every: 1}}},
		{Name: "5v5", Teams: []matchmaking.Team{{5, 5}, {5, 5}}},
		{Name: "ffa", Teams: []matchmaking.Team{{1, 1}}, FFA: &matchmaking.Team{2, 8}},
		{Name: "plain", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Game: a.Games.Game("slots", 1000, 1<<40, nil),
			Stakes: map[ledger.Currency][]int64{"usd": {100}}},
		{Name: "bet", Teams: []matchmaking.Team{{1, 1}, {1, 1}}, Game: g,
			Stakes: map[ledger.Currency][]int64{"usd": {100, 500}}},
	} {
		if _, err := s.Queue(c); err != nil {
			t.Fatal(err)
		}
	}
	return env{a, p, s}
}

type queueReq struct {
	Queue      string            `json:"queue"`
	Currency   ledger.Currency   `json:"currency,omitempty"`
	Stake      int64             `json:"stake,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Numbers    map[string]int64  `json:"numbers,omitempty"`
	IdemKey    string            `json:"idem_key"`
}

type status struct {
	Ticket  *matchmaking.Ticket `json:"ticket"`
	Matches []matchmaking.Match `json:"matches"`
}

func duel(key string) queueReq {
	return queueReq{Queue: "duel", Properties: map[string]string{"mode": "x"}, Numbers: map[string]int64{"level": 3}, IdemKey: key}
}

func queue(e env, u int64, r queueReq) (matchmaking.QueueResult, *realtime.ClientError) {
	return apptest.Call[matchmaking.QueueResult](e.a, u, "match.queue", r)
}

func statusOf(t *testing.T, e env, u int64) status {
	t.Helper()
	st, cerr := apptest.Call[status](e.a, u, "match.status", nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if st.Matches == nil {
		t.Fatal("matches must be a list, not null")
	}
	return st
}

func removedPush(e env, u int64) (reasons []string) {
	for _, raw := range e.a.Pushes(realtime.UserChannel(u)) {
		var m struct{ Type, Reason string }
		if json.Unmarshal(raw, &m) == nil && m.Type == "match.ticket_removed" {
			reasons = append(reasons, m.Reason)
		}
	}
	return
}

func ticketPushes(e env, u int64) (n int) {
	for _, raw := range e.a.Pushes(realtime.UserChannel(u)) {
		var m struct{ Type string }
		if json.Unmarshal(raw, &m) == nil && m.Type == "match.ticket" {
			n++
		}
	}
	return
}

func TestQueueAndStatus(t *testing.T) {
	e := setup(t)
	alice := e.a.User("alice")
	r, cerr := queue(e, alice, duel("k1"))
	if cerr != nil || r.Ticket == nil || r.MatchID != 0 || r.Removed != "" {
		t.Fatalf("queue: %+v %v", r, cerr)
	}
	st := statusOf(t, e, alice)
	if st.Ticket == nil || st.Ticket.ID != r.Ticket.ID || len(st.Ticket.Users) != 1 || st.Ticket.Users[0] != alice ||
		math.Abs(st.Ticket.Rating) > 1e-9 || st.Ticket.Numbers["level"] != 3 || st.Ticket.Properties["mode"] != "x" {
		t.Fatalf("status %+v", st.Ticket)
	}
	if n := ticketPushes(e, alice); n != 1 {
		t.Fatalf("ticket pushes %d", n)
	}
	again, cerr := queue(e, alice, duel("k1"))
	if cerr != nil || again.Ticket == nil || again.Ticket.ID != r.Ticket.ID {
		t.Fatalf("replay %+v %v", again, cerr)
	}
	if n := ticketPushes(e, alice); n != 1 {
		t.Fatalf("replay pushed: %d", n)
	}
	// a different payload under the same key still returns the stored outcome
	other := duel("k1")
	other.Queue = "ffa"
	if again, cerr = queue(e, alice, other); cerr != nil || again.Ticket == nil || again.Ticket.ID != r.Ticket.ID {
		t.Fatalf("replay other payload %+v %v", again, cerr)
	}
	if _, cerr = queue(e, alice, duel("k2")); cerr != matchmaking.ErrQueued {
		t.Fatalf("second key: %v", cerr)
	}
	if st := statusOf(t, e, e.a.User("bob")); st.Ticket != nil {
		t.Fatal("bob has a ticket")
	}
}

func TestQueueValidation(t *testing.T) {
	e := setup(t)
	alice := e.a.User("alice")
	bad := func(name string, r queueReq, want *realtime.ClientError) {
		t.Helper()
		if _, cerr := queue(e, alice, r); cerr == nil || cerr.Code != want.Code {
			t.Errorf("%s: got %v want %v", name, cerr, want)
		}
	}
	bad("unknown queue", queueReq{Queue: "nope", IdemKey: "a"}, matchmaking.ErrUnknownQueue)
	badPayload := &realtime.ClientError{Code: realtime.CodeBadPayload}
	bad("no key", queueReq{Queue: "ffa"}, badPayload)
	bad("long key", queueReq{Queue: "ffa", IdemKey: strings.Repeat("k", 65)}, badPayload)
	d := duel("b")
	d.Properties = map[string]string{"mode": "x", "extra": "y"}
	bad("undeclared property", d, gameconfig.ErrInvalid)
	d = duel("c")
	d.Properties = nil
	bad("missing property", d, gameconfig.ErrInvalid)
	d = duel("d")
	d.Numbers = map[string]int64{"level": 1, "xp": 2}
	bad("undeclared number", d, gameconfig.ErrInvalid)
	d = duel("e")
	d.Numbers = nil
	bad("missing number", d, gameconfig.ErrInvalid)
	bad("money in a free queue", queueReq{Queue: "ffa", Currency: "usd", Stake: 100, IdemKey: "f"}, gameconfig.ErrInvalid)
	bad("stake not listed", queueReq{Queue: "bet", Currency: "usd", Stake: 300, IdemKey: "g"}, gameconfig.ErrInvalid)
	bad("currency not listed", queueReq{Queue: "bet", Currency: "coin", Stake: 100, IdemKey: "h"}, gameconfig.ErrInvalid)
	bad("no stake", queueReq{Queue: "bet", IdemKey: "i"}, gameconfig.ErrInvalid)
	if st := statusOf(t, e, alice); st.Ticket != nil {
		t.Fatal("a refused call left a ticket")
	}
	// PvP games ignore Off; a game that is not PvP is unavailable
	e.a.Games.Off("usd")
	bad("game off", queueReq{Queue: "plain", Currency: "usd", Stake: 100, IdemKey: "j"}, gameconfig.ErrUnavailable)
	r, cerr := queue(e, alice, queueReq{Queue: "bet", Currency: "usd", Stake: 500, IdemKey: "k"})
	if cerr != nil || r.Ticket == nil || r.Ticket.Stake != 500 || r.Ticket.Currency != "usd" {
		t.Fatalf("staked: %+v %v", r, cerr)
	}
}

func TestQueueParty(t *testing.T) {
	e := setup(t)
	mk := func(leader int64, members ...int64) {
		t.Helper()
		pr, cerr := apptest.Call[party.Party](e.a, leader, "party.create", map[string]any{"open": true})
		if cerr != nil {
			t.Fatal(cerr)
		}
		for _, m := range members {
			if _, cerr := e.a.Call(m, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
				t.Fatal(cerr)
			}
		}
	}
	alice, bob, carol := e.a.User("alice"), e.a.User("bob"), e.a.User("carol")
	mk(alice, bob, carol)
	if _, cerr := queue(e, bob, queueReq{Queue: "5v5", IdemKey: "b"}); cerr != party.ErrNotLeader {
		t.Fatalf("member: %v", cerr)
	}
	r, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"})
	if cerr != nil || r.Ticket == nil || len(r.Ticket.Users) != 3 || r.Ticket.Leader != alice || r.Ticket.PartyID == 0 {
		t.Fatalf("party: %+v %v", r, cerr)
	}
	for _, u := range []int64{alice, bob, carol} {
		if st := statusOf(t, e, u); st.Ticket == nil || st.Ticket.ID != r.Ticket.ID {
			t.Fatalf("user %d status %+v", u, st.Ticket)
		}
		if n := ticketPushes(e, u); n != 1 {
			t.Fatalf("user %d pushes %d", u, n)
		}
	}
	if _, cerr := queue(e, carol, queueReq{Queue: "ffa", IdemKey: "c"}); cerr != party.ErrNotLeader {
		t.Fatalf("member again: %v", cerr)
	}

	big := make([]int64, 5)
	for i := range big {
		big[i] = e.a.User("big" + string(rune('a'+i)))
	}
	mk(big[0], big[1:]...)
	if _, cerr := queue(e, big[0], queueReq{Queue: "ffa", IdemKey: "d"}); cerr != matchmaking.ErrPartyTooBig {
		t.Fatalf("ffa: %v", cerr)
	}
	if _, cerr := queue(e, big[0], duel("e")); cerr != matchmaking.ErrPartyTooBig {
		t.Fatalf("duel: %v", cerr)
	}
	if _, cerr := e.a.Call(e.a.User("six"), "party.join", map[string]any{"party_id": mustParty(t, e, big[0])}); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := queue(e, big[0], queueReq{Queue: "5v5", IdemKey: "f"}); cerr != matchmaking.ErrPartyTooBig {
		t.Fatalf("party of 6 in 5v5: %v", cerr)
	}
}

func mustParty(t *testing.T, e env, u int64) int64 {
	t.Helper()
	ps, err := e.p.Of(context.Background(), u)
	if err != nil || len(ps) != 1 {
		t.Fatal(ps, err)
	}
	return ps[0].ID
}

func TestQueuePartyTooBigTeam(t *testing.T) {
	e := setup(t)
	if _, err := e.s.Queue(matchmaking.QueueConfig{Name: "3v3", Teams: []matchmaking.Team{{3, 3}, {3, 3}}}); err != nil {
		t.Fatal(err)
	}
	var us []int64
	for _, n := range []string{"a", "b", "c", "d"} {
		us = append(us, e.a.User(n))
	}
	pr, _ := apptest.Call[party.Party](e.a, us[0], "party.create", map[string]any{"open": true})
	for _, u := range us[1:] {
		if _, cerr := e.a.Call(u, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if _, cerr := queue(e, us[0], queueReq{Queue: "3v3", IdemKey: "x"}); cerr != matchmaking.ErrPartyTooBig {
		t.Fatalf("party of 4 in 3v3: %v", cerr)
	}
}

func TestQueuePartyChange(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.a.User("alice"), e.a.User("bob"), e.a.User("carol")
	pr, _ := apptest.Call[party.Party](e.a, alice, "party.create", map[string]any{"open": true})
	for _, u := range []int64{bob, carol} {
		if _, cerr := e.a.Call(u, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	r, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := e.a.Call(carol, "party.leave", nil); cerr != nil {
		t.Fatal(cerr)
	}
	for _, u := range []int64{alice, bob, carol} {
		if got := removedPush(e, u); len(got) != 1 || got[0] != "party_changed" {
			t.Fatalf("user %d removal pushes %v", u, got)
		}
		if st := statusOf(t, e, u); st.Ticket != nil {
			t.Fatalf("user %d still queued", u)
		}
	}
	// the replay reports why the ticket is gone
	again, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"})
	if cerr != nil || again.Ticket != nil || again.Removed != "party_changed" || r.Ticket == nil {
		t.Fatalf("replay %+v %v", again, cerr)
	}
	// the remaining pair can queue again
	if _, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "b"}); cerr != nil {
		t.Fatal(cerr)
	}
}

func TestQueueCancel(t *testing.T) {
	e := setup(t)
	alice := e.a.User("alice")
	if _, cerr := e.a.Call(alice, "match.cancel", nil); cerr != nil {
		t.Fatalf("cancel with no ticket: %v", cerr)
	}
	if _, cerr := queue(e, alice, duel("k1")); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := e.a.Call(alice, "match.cancel", nil); cerr != nil {
		t.Fatal(cerr)
	}
	if got := removedPush(e, alice); len(got) != 1 || got[0] != "cancelled" {
		t.Fatalf("pushes %v", got)
	}
	if st := statusOf(t, e, alice); st.Ticket != nil {
		t.Fatal("still queued")
	}
	r, cerr := queue(e, alice, duel("k1"))
	if cerr != nil || r.Ticket != nil || r.Removed != "cancelled" {
		t.Fatalf("replay after cancel: %+v %v", r, cerr)
	}
	if r, cerr = queue(e, alice, duel("k2")); cerr != nil || r.Ticket == nil {
		t.Fatalf("requeue: %+v %v", r, cerr)
	}
}

func TestQueueBusy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	alice := e.a.User("alice")
	var id int64
	if err := e.a.Pool.QueryRow(ctx, `INSERT INTO mm_matches (queue, ref, status, deadline) VALUES ('duel','r','running', now() + interval '1 hour') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Pool.Exec(ctx, `INSERT INTO mm_match_players (match_id, user_id, team, rating_mu, rating_sigma) VALUES ($1,$2,0,25,8)`, id, alice); err != nil {
		t.Fatal(err)
	}
	if _, cerr := queue(e, alice, duel("k1")); cerr != matchmaking.ErrBusy {
		t.Fatalf("busy: %v", cerr)
	}
	if st := statusOf(t, e, alice); len(st.Matches) != 1 || st.Matches[0].ID != id || st.Matches[0].Status != "running" ||
		len(st.Matches[0].Players) != 1 {
		t.Fatalf("matches %+v", st.Matches)
	}
	// another queue is fine
	if _, cerr := queue(e, alice, queueReq{Queue: "ffa", IdemKey: "k2"}); cerr != nil {
		t.Fatal(cerr)
	}
	if _, err := e.a.Pool.Exec(ctx, `UPDATE mm_matches SET status='settled'`); err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, e, alice); len(st.Matches) != 0 {
		t.Fatal("settled match listed")
	}
}

func TestRatingGet(t *testing.T) {
	e := setup(t)
	alice := e.a.User("alice")
	type rr struct {
		Mu, Sigma, Ordinal float64
		Matches            int
	}
	got, cerr := apptest.Call[rr](e.a, alice, "rating.get", map[string]string{"key": "duel"})
	if cerr != nil || got.Mu != 25 || math.Abs(got.Ordinal) > 1e-9 || got.Matches != 0 {
		t.Fatalf("%+v %v", got, cerr)
	}
	if _, err := e.a.Pool.Exec(context.Background(), `INSERT INTO mm_ratings (user_id, key, mu, sigma, matches) VALUES ($1,'duel',30,5,4)`, alice); err != nil {
		t.Fatal(err)
	}
	if got, _ = apptest.Call[rr](e.a, alice, "rating.get", map[string]string{"key": "duel"}); got.Mu != 30 || got.Ordinal != 15 || got.Matches != 4 {
		t.Fatalf("%+v", got)
	}
	r, err := e.s.Rating(context.Background(), alice, "duel")
	if err != nil || r.Mu != 30 {
		t.Fatalf("%+v %v", r, err)
	}
	// a ticket carries the mean ordinal
	if tk, _ := queue(e, alice, duel("k")); tk.Ticket == nil || math.Abs(tk.Ticket.Rating-15) > 1e-9 {
		t.Fatalf("ticket rating %+v", tk.Ticket)
	}
}

func TestQueueConcurrentSameKey(t *testing.T) {
	e := setup(t)
	alice := e.a.User("alice")
	ids := make(chan int64, 6)
	for range 6 {
		go func() {
			r, cerr := queue(e, alice, duel("same"))
			if cerr != nil || r.Ticket == nil {
				ids <- -1
				return
			}
			ids <- r.Ticket.ID
		}()
	}
	first := <-ids
	for range 5 {
		if id := <-ids; id != first || id < 1 {
			t.Fatalf("ticket ids differ: %d vs %d", id, first)
		}
	}
}

// cancelOnLeft cancels a call's context when the party push to the user who left goes out: a
// client hanging up right after its party.leave committed.
type cancelOnLeft struct {
	realtime.Transport
	cancel context.CancelFunc
}

func (c *cancelOnLeft) Publish(ctx context.Context, ch string, data []byte) error {
	if c.cancel != nil && strings.Contains(string(data), `"left":true`) {
		c.cancel()
	}
	return c.Transport.Publish(ctx, ch, data)
}

func TestQueuePartyLeaveCancelledCtx(t *testing.T) {
	a := apptest.New(t)
	tr := &cancelOnLeft{Transport: a.Realtime}
	a.Realtime = tr
	e := setupOn(t, a)
	alice, bob, carol := e.a.User("alice"), e.a.User("bob"), e.a.User("carol")
	pr, _ := apptest.Call[party.Party](e.a, alice, "party.create", map[string]any{"open": true})
	for _, u := range []int64{bob, carol} {
		if _, cerr := e.a.Call(u, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if _, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"}); cerr != nil {
		t.Fatal(cerr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tr.cancel = cancel
	if _, cerr := e.a.Router.Call(ctx, "party.leave", strconv.FormatInt(carol, 10), []byte("{}")); cerr != nil {
		t.Fatal(cerr)
	}
	if ctx.Err() == nil {
		t.Fatal("context not cancelled by the leave push")
	}
	for _, u := range []int64{alice, bob, carol} {
		if st := statusOf(t, e, u); st.Ticket != nil {
			t.Fatalf("user %d still queued after a leave on a cancelled context", u)
		}
		if got := removedPush(e, u); len(got) != 1 || got[0] != "party_changed" {
			t.Fatalf("user %d removal pushes %v", u, got)
		}
	}
}

// A party change the hook missed (a crash between commit and hook) is caught by the matcher.
func TestFormDropsChangedParty(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.a.User("alice"), e.a.User("bob"), e.a.User("carol")
	e.a.Transport.SetOnline(alice, bob, carol)
	pr, _ := apptest.Call[party.Party](e.a, alice, "party.create", map[string]any{"open": true})
	for _, u := range []int64{bob, carol} {
		if _, cerr := e.a.Call(u, "party.join", map[string]any{"party_id": pr.ID}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if _, cerr := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"}); cerr != nil {
		t.Fatal(cerr)
	}
	if _, err := e.a.Pool.Exec(context.Background(), `DELETE FROM party_members WHERE user_id = $1`, carol); err != nil {
		t.Fatal(err)
	}
	matchNow(t, e)
	for _, u := range []int64{alice, bob, carol} {
		if st := statusOf(t, e, u); st.Ticket != nil {
			t.Fatalf("user %d still queued", u)
		}
		if got := removedPush(e, u); len(got) != 1 || got[0] != "party_changed" {
			t.Fatalf("user %d removal pushes %v", u, got)
		}
	}
	if r, _ := queue(e, alice, queueReq{Queue: "5v5", IdemKey: "a"}); r.Removed != "party_changed" {
		t.Fatalf("replay %+v", r)
	}
}
