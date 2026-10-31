package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/auth"
)

// age moves a user's sign-up and last login back by the given durations (nil: never logged in).
func age(t *testing.T, a *apptest.App, id int64, created time.Duration, login *time.Duration) {
	t.Helper()
	var err error
	if login == nil {
		_, err = a.Pool.Exec(context.Background(),
			`UPDATE users SET created_at = now() - $2::interval, last_login_at = NULL WHERE id = $1`, id, created)
	} else {
		_, err = a.Pool.Exec(context.Background(),
			`UPDATE users SET created_at = now() - $2::interval, last_login_at = now() - $3::interval WHERE id = $1`, id, created, *login)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func dur(d time.Duration) *time.Duration { return &d }

func TestStats(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	const day = 24 * time.Hour
	a.User("today")                                       // new and active today
	age(t, a, a.User("week"), 3*day, dur(2*day))          // new this week, active this week
	age(t, a, a.User("month"), 20*day, dur(10*time.Hour)) // new this month, active today
	age(t, a, a.User("old"), 90*day, dur(40*day))         // neither
	age(t, a, a.User("never"), 2*time.Hour, nil)          // new today, never logged in

	got, err := a.Users.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := auth.Stats{Total: 5, New24h: 2, New7d: 3, New30d: 4, Active24h: 2, Active7d: 3, Active30d: 3}
	if got != want {
		t.Fatalf("Stats = %+v, want %+v", got, want)
	}
}

func TestSignups(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	const day = 24 * time.Hour
	a.User("a")
	a.User("b")
	age(t, a, a.User("c"), 2*day, nil)
	age(t, a, a.User("d"), 40*day, nil) // outside the window

	got, err := a.Users.Signups(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 {
		t.Fatalf("Signups(7) has %d days, want 7: %+v", len(got), got)
	}
	today := time.Now().UTC().Truncate(day)
	for i, d := range got {
		if want := today.AddDate(0, 0, i-6); !d.Day.Equal(want) {
			t.Fatalf("day %d = %v, want %v (oldest first, today last)", i, d.Day, want)
		}
	}
	var total int64
	for _, d := range got {
		total += d.Count
	}
	// Today has a and b, unless the test runs across UTC midnight; two days back has c.
	if total != 3 || got[6].Count+got[5].Count != 2 {
		t.Fatalf("Signups = %+v, want 3 in the window, 2 in the last day", got)
	}
	if _, err := a.Users.Signups(ctx, 0); err == nil {
		t.Fatal("Signups(0): want an error")
	}
	if _, err := a.Users.Signups(ctx, 367); err == nil {
		t.Fatal("Signups(367): want an error")
	}
}
