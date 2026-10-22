package gameconfig_test

import (
	"context"
	"errors"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/gameconfig"
)

func ptr(v int64) *int64 { return &v }

func TestSetLimitRefusesLoosen(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	a.Games.Game("g", 1000, 50_000, []string{"v"})
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	cases := []struct {
		name               string
		game, cur, variant string
		o                  gameconfig.LimitOverride
		want               error
	}{
		{"max amount", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(1001)}, gameconfig.ErrLoosens},
		{"max payout", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxPayout: ptr(50_001)}, gameconfig.ErrLoosens},
		{"min zero", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MinAmount: ptr(0)}, gameconfig.ErrBadOverride},
		{"min > max", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MinAmount: ptr(20), MaxAmount: ptr(10)}, gameconfig.ErrBadOverride},
		{"max amount >= payout", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(100), MaxPayout: ptr(100)}, gameconfig.ErrBadOverride},
		{"min above inherited max", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MinAmount: ptr(5000)}, gameconfig.ErrBadOverride},
		{"payout below inherited max amount", "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxPayout: ptr(100)}, gameconfig.ErrBadOverride},
		{"variant", "g", "usd", "x", gameconfig.LimitOverride{Enabled: true}, gameconfig.ErrBadOverride},
		{"currency", "g", "eur", "", gameconfig.LimitOverride{Enabled: true}, gameconfig.ErrBadOverride},
		{"empty game", "", "usd", "", gameconfig.LimitOverride{Enabled: true}, gameconfig.ErrBadOverride},
		{"unknown game", "nope", "usd", "", gameconfig.LimitOverride{Enabled: true}, gameconfig.ErrUnknownGame},
	}
	for _, c := range cases {
		if _, err := a.Games.SetLimit(ctx, tx, c.game, c.cur, c.variant, c.o); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := a.Games.SetGame(ctx, tx, "nope", true); !errors.Is(err, gameconfig.ErrUnknownGame) {
		t.Errorf("SetGame unknown: %v", err)
	}
	if _, err := a.Games.DeleteLimit(ctx, tx, "nope", "usd", ""); !errors.Is(err, gameconfig.ErrUnknownGame) {
		t.Errorf("DeleteLimit unknown: %v", err)
	}
}

func TestSetLimitRoundTrip(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	g := a.Games.Game("g", 1000, 50_000, []string{"v"})
	tx, _ := a.Pool.Begin(ctx)
	ch, err := a.Games.SetLimit(ctx, tx, "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(500)})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Before != nil {
		t.Fatalf("before: %v", ch.Before)
	}
	if after, ok := ch.After.(gameconfig.LimitOverride); !ok || *after.MaxAmount != 500 || !after.Enabled {
		t.Fatalf("after: %#v", ch.After)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Games.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, l, _ := g.Limits("usd", ""); l.MaxAmount != 500 {
		t.Fatalf("max amount %d", l.MaxAmount)
	}
	if _, lim := a.Games.Overrides(); *lim["g"]["usd"][""].MaxAmount != 500 {
		t.Fatalf("overrides: %#v", lim)
	}

	tx, _ = a.Pool.Begin(ctx)
	ch, err = a.Games.SetLimit(ctx, tx, "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(400)})
	if err != nil {
		t.Fatal(err)
	}
	if before, ok := ch.Before.(gameconfig.LimitOverride); !ok || *before.MaxAmount != 500 {
		t.Fatalf("before: %#v", ch.Before)
	}
	ch, err = a.Games.DeleteLimit(ctx, tx, "g", "usd", "")
	if err != nil || ch.After != nil {
		t.Fatalf("delete: %#v %v", ch, err)
	}
	if before, ok := ch.Before.(gameconfig.LimitOverride); !ok || *before.MaxAmount != 400 {
		t.Fatalf("delete before: %#v", ch.Before)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a.Games.Load(ctx)
	if _, l, _ := g.Limits("usd", ""); l.MaxAmount != 1000 {
		t.Fatalf("max amount %d", l.MaxAmount)
	}
	// Deleting a missing row is not an error; Before is nil.
	tx, _ = a.Pool.Begin(ctx)
	defer tx.Rollback(ctx)
	if ch, err := a.Games.DeleteLimit(ctx, tx, "g", "usd", ""); err != nil || ch.Before != nil || ch.After != nil {
		t.Fatalf("delete missing: %#v %v", ch, err)
	}
}

func TestSetGameRollback(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	g := a.Games.Game("g", 1000, 50_000, nil)
	tx, _ := a.Pool.Begin(ctx)
	ch, err := a.Games.SetGame(ctx, tx, "g", false)
	if err != nil || ch.Before != nil || ch.After != false {
		t.Fatalf("%#v %v", ch, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	a.Games.Load(ctx)
	if _, _, err := g.Limits("usd", ""); err != nil {
		t.Fatalf("still enabled: %v", err)
	}

	tx, _ = a.Pool.Begin(ctx)
	a.Games.SetGame(ctx, tx, "g", false)
	tx.Commit(ctx)
	tx, _ = a.Pool.Begin(ctx)
	ch, _ = a.Games.SetGame(ctx, tx, "g", true)
	tx.Commit(ctx)
	if ch.Before != false || ch.After != true {
		t.Fatalf("%#v", ch)
	}
	a.Games.Load(ctx)
	if settings, _ := a.Games.Overrides(); !settings["g"] {
		t.Fatalf("%v", settings)
	}
}

func TestSetLimitVariantInheritance(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	a.Games.Game("g", 1000, 50_000, []string{"v"})
	tx, _ := a.Pool.Begin(ctx)
	if _, err := a.Games.SetLimit(ctx, tx, "g", "usd", "v", gameconfig.LimitOverride{Enabled: true, MinAmount: ptr(200)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a.Games.Load(ctx)
	tx, _ = a.Pool.Begin(ctx)
	defer tx.Rollback(ctx)
	if _, err := a.Games.SetLimit(ctx, tx, "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(100)}); !errors.Is(err, gameconfig.ErrBadOverride) {
		t.Fatalf("got %v", err)
	}
}

func TestSetLimitRollback(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	a.Games.Game("g", 1000, 50_000, nil)
	tx, _ := a.Pool.Begin(ctx)
	if _, err := a.Games.SetLimit(ctx, tx, "g", "usd", "", gameconfig.LimitOverride{Enabled: true, MaxAmount: ptr(500)}); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	a.Games.Load(ctx)
	if _, lim := a.Games.Overrides(); len(lim) != 0 {
		t.Fatalf("rows after rollback: %#v", lim)
	}
}
