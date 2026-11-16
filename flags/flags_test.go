package flags_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/flags"

	"github.com/jackc/pgx/v5"
)

func setup(t *testing.T) (*apptest.App, *flags.Service) {
	t.Helper()
	a := apptest.New(t)
	s, err := flags.Register(context.Background(), a.App, flags.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}

// write runs f in a committed transaction, then applies it on s (as the console does).
func write(t *testing.T, a *apptest.App, s *flags.Service, f func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := f(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s.Applied(ctx)
	return nil
}

func create(t *testing.T, a *apptest.App, s *flags.Service, f flags.Flag) flags.Flag {
	t.Helper()
	var out flags.Flag
	if err := write(t, a, s, func(tx pgx.Tx) (err error) {
		out, err = s.Create(context.Background(), tx, f)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func getRPC(t *testing.T, a *apptest.App, user int64) string {
	t.Helper()
	out, cerr := a.Call(user, "flags.get", nil)
	if cerr != nil {
		t.Fatal(cerr)
	}
	return string(out)
}

func TestNewChecksConfig(t *testing.T) {
	if _, err := flags.New(nil, flags.Config{ReloadEvery: -time.Second}); err == nil {
		t.Fatal("negative ReloadEvery accepted")
	}
}

func TestCreateErrors(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	create(t, a, s, flags.Flag{Key: "new_lobby"})
	for name, c := range map[string]struct {
		f    flags.Flag
		want error
	}{
		"empty":     {flags.Flag{Key: ""}, flags.ErrBadKey},
		"upper":     {flags.Flag{Key: "New_Lobby"}, flags.ErrBadKey},
		"space":     {flags.Flag{Key: "new lobby"}, flags.ErrBadKey},
		"dash":      {flags.Flag{Key: "-x"}, flags.ErrBadKey},
		"long":      {flags.Flag{Key: strings.Repeat("a", 65)}, flags.ErrBadKey},
		"below 0":   {flags.Flag{Key: "x", Percent: -1}, flags.ErrBadPercent},
		"above 100": {flags.Flag{Key: "x", Percent: 101}, flags.ErrBadPercent},
		"type":      {flags.Flag{Key: "x", Type: "string"}, flags.ErrBadType},
		"duplicate": {flags.Flag{Key: "new_lobby"}, flags.ErrExists},
	} {
		err := write(t, a, s, func(tx pgx.Tx) error { _, err := s.Create(ctx, tx, c.f); return err })
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if err := write(t, a, s, func(tx pgx.Tx) error {
		_, err := s.Create(ctx, tx, flags.Flag{Key: strings.Repeat("a", 64)})
		return err
	}); err != nil {
		t.Errorf("64-character key: %v", err)
	}
	for name, f := range map[string]func(tx pgx.Tx) error{
		"update":  func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, flags.Flag{ID: 999999}); return err },
		"testers": func(tx pgx.Tx) error { _, _, err := s.SetTesters(ctx, tx, 999999, nil); return err },
		"delete":  func(tx pgx.Tx) error { _, err := s.Delete(ctx, tx, 999999); return err },
	} {
		if err := write(t, a, s, f); !errors.Is(err, flags.ErrNotFound) {
			t.Errorf("%s unknown id: %v", name, err)
		}
	}
	if _, err := s.Get(ctx, 999999); !errors.Is(err, flags.ErrNotFound) {
		t.Errorf("get unknown id: %v", err)
	}
}

func TestGetRPC(t *testing.T) {
	a, s := setup(t)
	alice, bob := a.User("alice"), a.User("bob")
	f := create(t, a, s, flags.Flag{Key: "new_lobby", Enabled: true, Testers: []int64{alice}})
	create(t, a, s, flags.Flag{Key: "shop_button"})
	if f.Type != "bool" || f.Version != 1 || !slices.Equal(f.Testers, []int64{alice}) {
		t.Fatalf("created: %+v", f)
	}
	if got := getRPC(t, a, alice); got != `{"flags":{"new_lobby":true,"shop_button":false}}` {
		t.Errorf("alice: %s", got)
	}
	if got := getRPC(t, a, bob); got != `{"flags":{"new_lobby":false,"shop_button":false}}` {
		t.Errorf("bob: %s", got)
	}
	if !s.Bool(bob, "missing", true) || s.Bool(bob, "missing", false) {
		t.Error("missing key ignores the fallback")
	}
	if !s.Bool(alice, "new_lobby", false) || s.Bool(bob, "new_lobby", true) {
		t.Error("Bool disagrees with flags.get")
	}
}

func TestSetTesters(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	alice, bob := a.User("alice"), a.User("bob")
	f := create(t, a, s, flags.Flag{Key: "new_lobby", Enabled: true})
	var before, after flags.Flag
	if err := write(t, a, s, func(tx pgx.Tx) (err error) {
		before, after, err = s.SetTesters(ctx, tx, f.ID, []int64{bob, alice, alice})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []int64{min(alice, bob), max(alice, bob)}
	if !slices.Equal(after.Testers, want) || after.Version != before.Version+1 || len(before.Testers) != 0 {
		t.Fatalf("before %+v after %+v", before, after)
	}
	err := write(t, a, s, func(tx pgx.Tx) error { _, _, err := s.SetTesters(ctx, tx, f.ID, []int64{alice, 999999}); return err })
	if !errors.Is(err, flags.ErrUnknownPlayer) {
		t.Fatalf("unknown player: %v", err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil || !slices.Equal(got.Testers, want) {
		t.Fatalf("after refused set: %+v %v", got, err)
	}
}

func TestOffKeepsRollout(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	alice := a.User("alice")
	f := create(t, a, s, flags.Flag{Key: "new_lobby", Enabled: true, Percent: 50, Testers: []int64{alice}})
	update := func(enabled bool) {
		t.Helper()
		if err := write(t, a, s, func(tx pgx.Tx) error {
			_, _, err := s.Update(ctx, tx, flags.Flag{ID: f.ID, Description: "d", Enabled: enabled, Percent: 50})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	update(false)
	got, err := s.Get(ctx, f.ID)
	if err != nil || got.Percent != 50 || !slices.Equal(got.Testers, []int64{alice}) || got.Description != "d" || got.Version != 2 {
		t.Fatalf("off: %+v %v", got, err)
	}
	if s.Bool(alice, "new_lobby", true) {
		t.Fatal("off flag is on for a tester")
	}
	update(true)
	if !s.Bool(alice, "new_lobby", false) {
		t.Fatal("back on: tester doesn't see it")
	}
}

func TestDeleteFallsBack(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	alice := a.User("alice")
	f := create(t, a, s, flags.Flag{Key: "new_lobby", Enabled: true, Testers: []int64{alice}})
	var before flags.Flag
	if err := write(t, a, s, func(tx pgx.Tx) (err error) { before, err = s.Delete(ctx, tx, f.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if before.Key != "new_lobby" || !slices.Equal(before.Testers, []int64{alice}) {
		t.Fatalf("deleted: %+v", before)
	}
	if _, ok := s.For(alice)["new_lobby"]; ok || !s.Bool(alice, "new_lobby", true) || s.Bool(alice, "new_lobby", false) {
		t.Fatal("deleted flag still read")
	}
	if got := getRPC(t, a, alice); got != `{"flags":{}}` {
		t.Fatalf("flags.get: %s", got)
	}
}

func TestReloadAcrossInstances(t *testing.T) {
	a, s1 := setup(t)
	ctx := context.Background()
	alice := a.User("alice")
	s2, err := flags.New(a.Pool, flags.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	create(t, a, s1, flags.Flag{Key: "new_lobby", Enabled: true, Percent: 100})
	if s2.Bool(alice, "new_lobby", false) {
		t.Fatal("s2 sees the flag before reloading")
	}
	s2.Applied(ctx)
	if !s2.Bool(alice, "new_lobby", false) {
		t.Fatal("s2 doesn't see the flag after reloading")
	}
	a.Pool.Close()
	s2.Applied(ctx) // fails: keeps the last snapshot
	if !s2.Bool(alice, "new_lobby", false) {
		t.Fatal("a failed reload dropped the flags")
	}
}

func TestStaleWritesRefused(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	alice := a.User("alice")
	f := create(t, a, s, flags.Flag{Key: "new_lobby"})
	update := func(version int64, percent int) error {
		return write(t, a, s, func(tx pgx.Tx) error {
			_, _, err := s.Update(ctx, tx, flags.Flag{ID: f.ID, Version: version, Enabled: true, Percent: percent})
			return err
		})
	}
	testers := func(version int64) error {
		return write(t, a, s, func(tx pgx.Tx) error {
			_, _, err := s.SetTestersAt(ctx, tx, f.ID, version, []int64{alice})
			return err
		})
	}
	if err := update(1, 10); err != nil { // the version it was read at: saved, now 2
		t.Fatal(err)
	}
	if err := update(1, 20); !errors.Is(err, flags.ErrStale) {
		t.Fatalf("stale update: %v", err)
	}
	if err := testers(1); !errors.Is(err, flags.ErrStale) {
		t.Fatalf("stale testers: %v", err)
	}
	if err := testers(2); err != nil { // now 3
		t.Fatal(err)
	}
	if err := update(0, 30); err != nil { // 0: no check
		t.Fatal(err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil || got.Percent != 30 || got.Version != 4 || !slices.Equal(got.Testers, []int64{alice}) {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestErrorsNameTheProblem(t *testing.T) {
	a, s := setup(t)
	ctx := context.Background()
	alice := a.User("alice")
	f := create(t, a, s, flags.Flag{Key: "new_lobby"})
	err := write(t, a, s, func(tx pgx.Tx) error {
		_, _, err := s.SetTesters(ctx, tx, f.ID, []int64{alice, 999998, 999999})
		return err
	})
	if !errors.Is(err, flags.ErrUnknownPlayer) || !strings.Contains(err.Error(), "999998, 999999") {
		t.Fatalf("unknown testers: %v", err)
	}
	err = write(t, a, s, func(tx pgx.Tx) error {
		_, err := s.Create(ctx, tx, flags.Flag{Key: "x", Testers: []int64{999997}})
		return err
	})
	if !errors.Is(err, flags.ErrUnknownPlayer) || !strings.Contains(err.Error(), "999997") {
		t.Fatalf("unknown tester on create: %v", err)
	}
	if strings.Contains(flags.ErrBadKey.Error(), "^") {
		t.Fatalf("ErrBadKey shows a regex: %v", flags.ErrBadKey)
	}
}
