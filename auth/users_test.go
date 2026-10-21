package auth

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"

	"github.com/amir1430/gamify/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a real Postgres (TEST_DATABASE_URL).
func users(t *testing.T) *Users {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(context.Background(), pool, db.Migrations, "goose_platform"); err != nil {
		t.Fatal(err)
	}
	return NewUsers(pool)
}

// subject is unique per test run: the database is shared between runs.
func subject() string { return fmt.Sprint(rand.Int64()) }

func TestUpsertSameIdentityOneUser(t *testing.T) {
	u, ctx := users(t), context.Background()
	id := Identity{Provider: "telegram", Subject: subject(), Username: "alice", DisplayName: "Alice", Lang: "en"}
	first, err := u.Upsert(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	id.DisplayName = "Alice B"
	second, err := u.Upsert(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.ID != second.ID {
		t.Fatalf("first %+v, second %+v: want one user, created once", first, second)
	}
	got, err := u.Get(ctx, first.ID)
	if err != nil || len(got) != 1 || got[0].DisplayName != "Alice B" || got[0].LastLoginAt == nil {
		t.Fatalf("get = %+v %v: want the refreshed name", got, err)
	}
	// A login whose provider sends no name keeps the stored one.
	id.Username, id.DisplayName = "", ""
	if _, err := u.Upsert(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, _ := u.Get(ctx, first.ID); got[0].Username != "alice" || got[0].DisplayName != "Alice B" {
		t.Fatalf("names after a nameless login = %+v", got[0])
	}
	// The same subject at another provider is another person.
	other, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: id.Subject})
	if err != nil || other.ID == first.ID {
		t.Fatalf("other provider = %+v %v: want a new user", other, err)
	}
}

// Two first logins at once with the same identity race on the primary key: both get the same user.
func TestUpsertConcurrentFirstLogin(t *testing.T) {
	u, ctx := users(t), context.Background()
	id := Identity{Provider: "telegram", Subject: subject()}
	ids := make([]int64, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			l, err := u.Upsert(ctx, id)
			if err != nil {
				t.Error(err)
			}
			ids[i] = l.ID
		})
	}
	wg.Wait()
	for _, got := range ids {
		if got != ids[0] || got == 0 {
			t.Fatalf("ids = %v: want one user", ids)
		}
	}
}

func TestLink(t *testing.T) {
	u, ctx := users(t), context.Background()
	alice, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: subject()})
	if err != nil {
		t.Fatal(err)
	}
	tg := Identity{Provider: "telegram", Subject: subject()}
	if err := u.Link(ctx, alice.ID, tg); err != nil {
		t.Fatal(err)
	}
	if err := u.Link(ctx, alice.ID, tg); err != nil {
		t.Fatalf("relinking her own identity: %v", err)
	}
	if l, err := u.Upsert(ctx, tg); err != nil || l.ID != alice.ID || l.Created {
		t.Fatalf("login with the linked identity = %+v %v: want alice", l, err)
	}
	if s, err := u.Subject(ctx, alice.ID, "telegram"); err != nil || s != tg.Subject {
		t.Fatalf("subject = %q %v", s, err)
	}

	if err := u.Link(ctx, alice.ID, Identity{Provider: "telegram", Subject: subject()}); !errors.Is(err, ErrProviderLinked) {
		t.Fatalf("a second telegram identity for alice = %v, want ErrProviderLinked", err)
	}

	bob, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: subject()})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Link(ctx, bob.ID, tg); !errors.Is(err, ErrIdentityTaken) {
		t.Fatalf("linking alice's identity to bob = %v, want ErrIdentityTaken", err)
	}
	if _, err := u.Subject(ctx, bob.ID, "telegram"); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("bob's telegram = %v, want ErrNoIdentity", err)
	}
}

func TestUnlink(t *testing.T) {
	u, ctx := users(t), context.Background()
	alice, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: subject()})
	if err != nil {
		t.Fatal(err)
	}
	stolen := Identity{Provider: "telegram", Subject: subject()}
	if err := u.Link(ctx, alice.ID, stolen); err != nil {
		t.Fatal(err)
	}
	if err := u.Unlink(ctx, alice.ID, "telegram"); err != nil {
		t.Fatal(err)
	}
	if l, err := u.Upsert(ctx, stolen); err != nil || l.ID == alice.ID || !l.Created {
		t.Fatalf("login with the unlinked identity = %+v %v: want a new user, not alice", l, err)
	}
	if err := u.Unlink(ctx, alice.ID, "telegram"); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("unlinking again = %v, want ErrNoIdentity", err)
	}
	if err := u.Unlink(ctx, alice.ID, "custom"); !errors.Is(err, ErrLastIdentity) {
		t.Fatalf("unlinking alice's only login = %v, want ErrLastIdentity", err)
	}
}

// Two unlinks at once of a user's last two logins: one must be refused, or the user is locked out.
func TestUnlinkConcurrentKeepsOne(t *testing.T) {
	u, ctx := users(t), context.Background()
	l, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: subject()})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Link(ctx, l.ID, Identity{Provider: "telegram", Subject: subject()}); err != nil {
		t.Fatal(err)
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, p := range []string{"custom", "telegram"} {
		wg.Go(func() { errs[i] = u.Unlink(ctx, l.ID, p) })
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("unlinks = %v: want exactly one refused", errs)
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrLastIdentity) {
			t.Fatalf("refusal = %v, want ErrLastIdentity", err)
		}
	}
}

func TestIdentities(t *testing.T) {
	u, ctx := users(t), context.Background()
	custom, tg := subject(), subject()
	alice, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: custom})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Link(ctx, alice.ID, Identity{Provider: "telegram", Subject: tg}); err != nil {
		t.Fatal(err)
	}
	got, err := u.Identities(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Provider != "custom" || got[0].Subject != custom ||
		got[1].Provider != "telegram" || got[1].Subject != tg || got[0].LinkedAt.IsZero() || got[1].LinkedAt.IsZero() {
		t.Fatalf("Identities = %+v, want custom and telegram in order with times", got)
	}
	if none, err := u.Identities(ctx, -1); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("unknown user = %v %v, want empty non-nil slice", none, err)
	}
}

// UnlinkTx leaves the commit to the caller: a rollback keeps the identity.
func TestUnlinkTxRollback(t *testing.T) {
	u, ctx := users(t), context.Background()
	alice, err := u.Upsert(ctx, Identity{Provider: "custom", Subject: subject()})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Link(ctx, alice.ID, Identity{Provider: "telegram", Subject: subject()}); err != nil {
		t.Fatal(err)
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.UnlinkTx(ctx, tx, alice.ID, "telegram"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, err := u.Identities(ctx, alice.ID); err != nil || len(ids) != 2 {
		t.Fatalf("after rollback = %+v %v, want both identities", ids, err)
	}
}
