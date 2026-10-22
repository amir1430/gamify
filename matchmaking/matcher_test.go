package matchmaking

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func queue(t *testing.T, c QueueConfig) *Queue {
	t.Helper()
	if c.Name == "" {
		c.Name = "q"
	}
	if c.Teams == nil {
		c.Teams = []Team{{1, 1}, {1, 1}}
	}
	q, err := (&Service{}).Queue(c)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// tk is ticket id with a party of n users (IDs id*100+1..), queued at t0 plus id ms.
func tk(id int64, n int) Ticket {
	t := Ticket{ID: id, QueuedAt: t0.Add(time.Duration(id) * time.Millisecond)}
	for j := 1; j <= n; j++ {
		t.Users = append(t.Users, id*100+int64(j))
	}
	return t
}

func ticketIDs(fs []formed) [][]int64 {
	var out [][]int64
	for _, f := range fs {
		out = append(out, f.Tickets)
	}
	return out
}

func wantIDs(t *testing.T, got []formed, want ...[]int64) {
	t.Helper()
	if g := ticketIDs(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("matches %v, want %v", g, want)
	}
}

func TestForm1v1(t *testing.T) {
	q := queue(t, QueueConfig{})
	got := form(q, []Ticket{tk(1, 1), tk(2, 1), tk(3, 1)}, t0)
	wantIDs(t, got, []int64{1, 2})
	if !reflect.DeepEqual(got[0].Teams, [][]int64{{101}, {201}}) {
		t.Fatalf("teams %v", got[0].Teams)
	}
}

func TestFormProperties(t *testing.T) {
	q := queue(t, QueueConfig{Properties: []string{"region"}})
	ts := []Ticket{tk(1, 1), tk(2, 1), tk(3, 1)}
	ts[0].Properties = map[string]string{"region": "eu"}
	ts[1].Properties = map[string]string{"region": "us"}
	ts[2].Properties = map[string]string{"region": "eu"}
	wantIDs(t, form(q, ts, t0), []int64{1, 3})

	ts[1].Properties, ts[2].Properties = nil, nil
	ts[1].Server = map[string]string{"tier": "gold"}
	wantIDs(t, form(q, ts[1:], t0)) // Server differs
}

func TestFormStakes(t *testing.T) {
	q := queue(t, QueueConfig{})
	ts := []Ticket{tk(1, 1), tk(2, 1), tk(3, 1), tk(4, 1)}
	ts[0].Currency, ts[0].Stake = "coin", 100
	ts[1].Currency, ts[1].Stake = "coin", 500
	ts[2].Currency, ts[2].Stake = "gem", 100
	ts[3].Currency, ts[3].Stake = "coin", 500
	wantIDs(t, form(q, ts, t0), []int64{2, 4})
	wantIDs(t, form(q, ts[:3], t0))
}

func TestFormParties5v5(t *testing.T) {
	q := queue(t, QueueConfig{Teams: []Team{{5, 5}, {5, 5}}})
	got := form(q, []Ticket{tk(1, 5), tk(2, 3), tk(3, 2), tk(4, 5)}, t0)
	wantIDs(t, got, []int64{1, 4})
	if len(got[0].Teams[0]) != 5 || len(got[0].Teams[1]) != 5 {
		t.Fatalf("teams %v", got[0].Teams)
	}
	// 3+2 stay until two more units arrive.
	wantIDs(t, form(q, []Ticket{tk(2, 3), tk(3, 2)}, t0))
	wantIDs(t, form(q, []Ticket{tk(2, 3), tk(3, 2), tk(5, 3), tk(6, 2)}, t0), []int64{2, 5, 3, 6})

	got = form(q, []Ticket{tk(1, 3), tk(2, 2), tk(3, 5)}, t0)
	wantIDs(t, got, []int64{1, 3, 2})
	if !reflect.DeepEqual(got[0].Teams, [][]int64{{101, 102, 103, 201, 202}, {301, 302, 303, 304, 305}}) {
		t.Fatalf("teams %v", got[0].Teams)
	}
}

func TestFormFillAfter(t *testing.T) {
	q := queue(t, QueueConfig{Teams: []Team{{1, 1}}, FFA: &Team{2, 8}})
	ts := []Ticket{tk(1, 1), tk(2, 1), tk(3, 1)}
	for i := range ts {
		ts[i].QueuedAt = t0
	}
	wantIDs(t, form(q, ts, t0.Add(5*time.Second)))
	got := form(q, ts, t0.Add(10*time.Second))
	wantIDs(t, got, []int64{1, 2, 3})
	if !reflect.DeepEqual(got[0].Teams, [][]int64{{101}, {201}, {301}}) {
		t.Fatalf("teams %v", got[0].Teams)
	}
}

func TestFormRankedWindow(t *testing.T) {
	q := queue(t, QueueConfig{Ranked: true, RatingWindow: 5, RatingWiden: 5})
	ts := []Ticket{tk(1, 1), tk(2, 1)}
	ts[0].Rating, ts[1].Rating = 10, 30
	wantIDs(t, form(q, ts, t0))
	wantIDs(t, form(q, ts, t0.Add(40*time.Second)), []int64{1, 2})
}

func TestFormRankedBalance(t *testing.T) {
	q := queue(t, QueueConfig{Teams: []Team{{2, 2}, {2, 2}}, Ranked: true, RatingWindow: 100})
	ts := []Ticket{tk(1, 1), tk(2, 1), tk(3, 1), tk(4, 1)}
	for i, r := range []float64{40, 30, 20, 10} {
		ts[i].Rating = r
	}
	got := form(q, ts, t0)
	wantIDs(t, got, []int64{1, 2, 3, 4})
	if !reflect.DeepEqual(got[0].Teams, [][]int64{{101, 401}, {201, 301}}) {
		t.Fatalf("teams %v", got[0].Teams)
	}
}

func TestFormNumbers(t *testing.T) {
	q := queue(t, QueueConfig{Numbers: map[string]NumberRule{"level": {Diff: 2, Widen: 1, Every: 10 * time.Second}}})
	ts := []Ticket{tk(1, 1), tk(2, 1)}
	ts[0].Numbers = map[string]int64{"level": 3}
	ts[1].Numbers = map[string]int64{"level": 6}
	ts[0].QueuedAt = t0
	wantIDs(t, form(q, ts, t0))
	wantIDs(t, form(q, ts, t0.Add(10*time.Second)), []int64{1, 2})
}

func TestFormCompatible(t *testing.T) {
	q := queue(t, QueueConfig{Compatible: func(a, b Ticket) bool { return a.ID+b.ID != 3 }})
	wantIDs(t, form(q, []Ticket{tk(1, 1), tk(2, 1)}, t0))
	wantIDs(t, form(q, []Ticket{tk(1, 1), tk(2, 1), tk(3, 1)}, t0), []int64{1, 3})

	// Every pair, not just the anchor: 2 and 3 clash in a 3-player FFA.
	ffa := queue(t, QueueConfig{Name: "ffa", Teams: []Team{{1, 1}}, FFA: &Team{3, 3},
		Compatible: func(a, b Ticket) bool { return a.ID*b.ID != 6 }})
	wantIDs(t, form(ffa, []Ticket{tk(1, 1), tk(2, 1), tk(3, 1), tk(4, 1)}, t0), []int64{1, 2, 4})
}

func TestFormAsymmetric(t *testing.T) {
	q := queue(t, QueueConfig{Teams: []Team{{1, 1}, {4, 4}}})
	got := form(q, []Ticket{tk(1, 1), tk(2, 1), tk(3, 1), tk(4, 1), tk(5, 1)}, t0)
	wantIDs(t, got, []int64{1, 2, 3, 4, 5})
	if len(got[0].Teams[0]) != 1 || len(got[0].Teams[1]) != 4 {
		t.Fatalf("teams %v", got[0].Teams)
	}
}
