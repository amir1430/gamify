package flags

import (
	"context"
	"testing"

	"github.com/amir1430/gamify/apptest"
)

// A reload that read before a console write but finishes after that write's Applied must not
// put the old flags back.
func TestStaleReloadDoesNotUndoApplied(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	s, err := Register(ctx, a.App, Config{})
	if err != nil {
		t.Fatal(err)
	}
	alice := a.User("alice")
	old, gen, err := s.read(ctx) // the loop's reload, paused between its read and its store
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, tx, Flag{Key: "new_lobby", Enabled: true, Percent: 100}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s.Applied(ctx)
	s.install(old, gen) // the paused reload finishes
	if !s.Bool(alice, "new_lobby", false) {
		t.Fatal("a stale reload undid the write")
	}
}
