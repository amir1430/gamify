package restrict_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/realtime"
	"github.com/amir1430/gamify/restrict"
)

func TestNewChecksConfig(t *testing.T) {
	for name, cfg := range map[string]restrict.Config{
		"empty name":     {Blocks: map[string][]string{"": {"a.b"}}},
		"no methods":     {Blocks: map[string][]string{"bets": nil}},
		"empty method":   {Blocks: map[string][]string{"bets": {""}}},
		"negative every": {ReloadEvery: -time.Second},
	} {
		if _, err := restrict.New(nil, cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRestrictions(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	s, err := restrict.Register(ctx, a.App, restrict.Config{Blocks: map[string][]string{
		"bets": {"game.bet"}, "trades": {"shop.buy", "game.bet"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"game.bet", "shop.buy", "game.cashout"} {
		a.Router.Handle(m, realtime.JSON(func(context.Context, int64, struct{}) (struct{}, error) { return struct{}{}, nil }))
	}
	alice, bob := a.User("alice"), a.User("bob")
	set := func(user int64, r restrict.Restriction) (before, after restrict.Restriction) {
		t.Helper()
		tx, err := a.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if before, after, err = s.Set(ctx, tx, user, r); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		s.Applied(ctx, user, after)
		return before, after
	}
	code := func(user int64, method string) uint32 {
		t.Helper()
		if _, cerr := a.Call(user, method, nil); cerr != nil {
			return cerr.Code
		}
		return 0
	}

	// A block refuses its methods only: the player can still finish what is in play.
	before, after := set(alice, restrict.Restriction{Blocks: []string{"bets"}, Note: "abuse"})
	if before.Any() || after.Version != 1 || after.Note != "abuse" {
		t.Fatalf("first set: before %+v after %+v", before, after)
	}
	if code(alice, "game.bet") != 1108 || code(alice, "shop.buy") != 0 || code(alice, "game.cashout") != 0 || code(bob, "game.bet") != 0 {
		t.Fatal("bets block")
	}
	if err := s.Login(ctx, alice); err != nil {
		t.Fatalf("a block refuses login: %v", err)
	}
	// Suspended: no login, every call refused; and the player's connections are dropped.
	set(alice, restrict.Restriction{Suspended: true})
	if code(alice, "game.cashout") != 1108 || !errors.Is(s.Login(ctx, alice), restrict.ErrSuspended) {
		t.Fatal("suspended")
	}
	if d := a.Transport.Disconnected(); len(d) != 1 || d[0] != alice {
		t.Fatalf("disconnected %v", d)
	}
	// Until in the past: nothing applies.
	past := time.Now().Add(-time.Minute)
	set(alice, restrict.Restriction{Suspended: true, Until: &past})
	if code(alice, "game.bet") != 0 || s.Login(ctx, alice) != nil {
		t.Fatal("expired restriction still applies")
	}
	// Lifting (the zero Restriction) clears it; an unknown block is refused.
	if _, after := set(alice, restrict.Restriction{}); after.Any() {
		t.Fatalf("lifted: %+v", after)
	}
	tx, _ := a.Pool.Begin(ctx)
	if _, _, err := s.Set(ctx, tx, bob, restrict.Restriction{Blocks: []string{"chat"}}); !errors.Is(err, restrict.ErrUnknownBlock) {
		t.Fatalf("unknown block: %v", err)
	}
	tx.Rollback(ctx)
	// Another instance's change arrives on reload.
	other, _ := restrict.New(a.Pool, restrict.Config{Blocks: map[string][]string{"bets": {"game.bet"}, "trades": {"shop.buy", "game.bet"}}})
	set(bob, restrict.Restriction{Blocks: []string{"trades"}})
	if err := other.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if other.Check(ctx, bob, "shop.buy") == nil || other.Check(ctx, bob, "game.cashout") != nil {
		t.Fatal("reloaded copy")
	}
	if rows, err := s.List(ctx, 0, 10); err != nil || len(rows) != 2 || rows[0].UserID != bob {
		t.Fatalf("list %+v %v", rows, err)
	}
}
