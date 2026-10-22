package party_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/realtime"
)

type push struct {
	Type    string          `json:"type"`
	Party   party.Party     `json:"party"`
	Left    bool            `json:"left"`
	PartyID int64           `json:"party_id"`
	UserID  int64           `json:"user_id"`
	From    int64           `json:"from"`
	Kind    string          `json:"kind"`
	Data    json.RawMessage `json:"data"`
}

func pushes(a *apptest.App, user int64, typ string) []push {
	var out []push
	for _, raw := range a.Pushes(realtime.UserChannel(user)) {
		var p push
		if json.Unmarshal(raw, &p) == nil && p.Type == typ {
			out = append(out, p)
		}
	}
	return out
}

func last(t *testing.T, a *apptest.App, user int64, typ string) push {
	t.Helper()
	ps := pushes(a, user, typ)
	if len(ps) == 0 {
		t.Fatalf("user %d got no %s push", user, typ)
	}
	return ps[len(ps)-1]
}

func setup(t *testing.T, cfg party.Config) (*apptest.App, *party.Service) {
	a := apptest.New(t)
	s, err := party.Register(context.Background(), a.App, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}

func call(t *testing.T, a *apptest.App, u int64, method string, req any) (party.Party, *realtime.ClientError) {
	t.Helper()
	return apptest.Call[party.Party](a, u, method, req)
}

func must(t *testing.T, cerr *realtime.ClientError) {
	t.Helper()
	if cerr != nil {
		t.Fatal(cerr)
	}
}

func wantErr(t *testing.T, got, want *realtime.ClientError) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPartyLifecycle(t *testing.T) {
	a, s := setup(t, party.Config{})
	alice, bob, carol, dave := a.User("alice"), a.User("bob"), a.User("carol"), a.User("dave")
	p, cerr := call(t, a, alice, "party.create", map[string]any{"max": 3, "open": true})
	must(t, cerr)
	_, cerr = call(t, a, bob, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	for _, u := range []int64{alice, bob} {
		if m := last(t, a, u, "party").Party.Members; !slices.Equal(m, []int64{alice, bob}) {
			t.Fatalf("user %d push members %v", u, m)
		}
	}
	_, cerr = call(t, a, carol, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	_, cerr = call(t, a, dave, "party.join", map[string]any{"party_id": p.ID})
	wantErr(t, cerr, party.ErrFull)

	_, cerr = call(t, a, alice, "party.leave", nil)
	must(t, cerr)
	got, err := s.Get(context.Background(), p.ID)
	if err != nil || got.LeaderID != bob || !slices.Equal(got.Members, []int64{bob, carol}) {
		t.Fatalf("after leave: %+v %v", got, err)
	}
	if !last(t, a, alice, "party").Left {
		t.Fatal("alice should get left:true")
	}
	for _, u := range []int64{bob, carol} {
		_, cerr = call(t, a, u, "party.leave", nil)
		must(t, cerr)
	}
	if _, err := s.Get(context.Background(), p.ID); err != party.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if ps, err := s.Of(context.Background(), bob); err != nil || len(ps) != 0 {
		t.Fatalf("Of: %v %v", ps, err)
	}
}

func TestPartyClosed(t *testing.T) {
	a, s := setup(t, party.Config{})
	alice, bob := a.User("alice"), a.User("bob")
	p, cerr := call(t, a, alice, "party.create", map[string]any{"max": 4})
	must(t, cerr)
	got, cerr := call(t, a, bob, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	if slices.Contains(got.Members, bob) {
		t.Fatal("bob joined a closed party")
	}
	if r := last(t, a, alice, "party.request"); r.PartyID != p.ID || r.UserID != bob {
		t.Fatalf("request push %+v", r)
	}
	_, cerr = call(t, a, bob, "party.accept", map[string]any{"user_id": bob})
	wantErr(t, cerr, party.ErrNotLeader)
	_, cerr = call(t, a, alice, "party.accept", map[string]any{"user_id": bob})
	must(t, cerr)
	if g, _ := s.Get(context.Background(), p.ID); !slices.Equal(g.Members, []int64{alice, bob}) {
		t.Fatalf("members %v", g.Members)
	}
}

func TestPartyOnePerUser(t *testing.T) {
	for _, per := range []int{0, 2} {
		a, _ := setup(t, party.Config{PartiesPerUser: per})
		alice, bob, carol := a.User("alice"), a.User("bob"), a.User("carol")
		p1, cerr := call(t, a, alice, "party.create", map[string]any{"max": 4, "open": true})
		must(t, cerr)
		p2, cerr := call(t, a, carol, "party.create", map[string]any{"max": 4, "open": true})
		must(t, cerr)
		_, cerr = call(t, a, bob, "party.join", map[string]any{"party_id": p1.ID})
		must(t, cerr)
		_, cerr = call(t, a, bob, "party.join", map[string]any{"party_id": p2.ID})
		if per == 0 {
			wantErr(t, cerr, party.ErrInParty)
		} else {
			must(t, cerr)
		}
	}
}

func TestPartyKickLead(t *testing.T) {
	a, _ := setup(t, party.Config{})
	alice, bob, carol := a.User("alice"), a.User("bob"), a.User("carol")
	p, _ := call(t, a, alice, "party.create", map[string]any{"max": 4, "open": true})
	for _, u := range []int64{bob, carol} {
		_, cerr := call(t, a, u, "party.join", map[string]any{"party_id": p.ID})
		must(t, cerr)
	}
	_, cerr := call(t, a, bob, "party.kick", map[string]any{"user_id": carol})
	wantErr(t, cerr, party.ErrNotLeader)
	_, cerr = call(t, a, alice, "party.kick", map[string]any{"user_id": bob})
	must(t, cerr)
	if !last(t, a, bob, "party").Left {
		t.Fatal("bob should get left:true")
	}
	got, cerr := call(t, a, alice, "party.lead", map[string]any{"user_id": carol})
	must(t, cerr)
	if got.LeaderID != carol {
		t.Fatalf("leader %d", got.LeaderID)
	}
}

func TestPartyLocked(t *testing.T) {
	a, s := setup(t, party.Config{})
	alice, bob, carol := a.User("alice"), a.User("bob"), a.User("carol")
	ctx := context.Background()
	p, _ := call(t, a, alice, "party.create", map[string]any{"max": 4, "open": true})
	_, cerr := call(t, a, bob, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	for range 2 { // idempotent for the same holder
		if err := s.Lock(ctx, p.ID, "t:1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Lock(ctx, p.ID, "t:2"); err != party.ErrLocked {
		t.Fatalf("other holder: %v", err)
	}
	_, cerr = call(t, a, carol, "party.join", map[string]any{"party_id": p.ID})
	wantErr(t, cerr, party.ErrLocked)
	_, cerr = call(t, a, bob, "party.leave", nil)
	wantErr(t, cerr, party.ErrLocked)
	_, cerr = call(t, a, alice, "party.kick", map[string]any{"user_id": bob})
	wantErr(t, cerr, party.ErrLocked)
	if g, _ := s.Get(ctx, p.ID); g.Locked != "t:1" {
		t.Fatalf("locked %q", g.Locked)
	}
	if err := s.Unlock(ctx, p.ID, "t:1"); err != nil {
		t.Fatal(err)
	}
	_, cerr = call(t, a, bob, "party.leave", nil)
	must(t, cerr)
}

func TestPartyMessages(t *testing.T) {
	a, _ := setup(t, party.Config{})
	alice, bob, carol := a.User("alice"), a.User("bob"), a.User("carol")
	p, _ := call(t, a, alice, "party.create", map[string]any{"max": 4, "open": true})
	_, cerr := call(t, a, bob, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	send := func(u int64, data string) *realtime.ClientError {
		_, cerr := a.Call(u, "party.send", map[string]any{"party_id": p.ID, "kind": "chat", "data": json.RawMessage(data)})
		return cerr
	}
	must(t, send(alice, `"hi"`))
	for _, u := range []int64{alice, bob} {
		m := last(t, a, u, "party.message")
		if m.PartyID != p.ID || m.From != alice || m.Kind != "chat" || string(m.Data) != `"hi"` {
			t.Fatalf("user %d got %+v", u, m)
		}
	}
	wantErr(t, send(carol, `"x"`), party.ErrNotMember)
	wantErr(t, send(bob, `"`+strings.Repeat("a", 4095)+`"`), gameconfig.ErrInvalid) // 4097 bytes
	for range 3 {                                                                   // alice has sent 1
		must(t, send(alice, `1`))
	}
	must(t, send(bob, `1`)) // per user
	must(t, send(alice, `1`))
	wantErr(t, send(alice, `1`), party.ErrRateLimited)
}

func TestPartyOnChange(t *testing.T) {
	a, s := setup(t, party.Config{})
	alice, bob := a.User("alice"), a.User("bob")
	var seen [][]int64
	s.OnChange(func(_ context.Context, p party.Party) { seen = append(seen, p.Members) })
	p, _ := call(t, a, alice, "party.create", map[string]any{"max": 4, "open": true})
	_, cerr := call(t, a, bob, "party.join", map[string]any{"party_id": p.ID})
	must(t, cerr)
	_, cerr = call(t, a, bob, "party.leave", nil)
	must(t, cerr)
	n := len(seen)
	if n < 2 || !slices.Equal(seen[n-2], []int64{alice, bob}) || !slices.Equal(seen[n-1], []int64{alice}) {
		t.Fatalf("hook saw %v", seen)
	}
}
