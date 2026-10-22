package matchmaking_test

import (
	"context"
	"testing"

	"github.com/amir1430/gamify/app"

	"github.com/prometheus/client_golang/prometheus"
)

// metric is the value of the counter or gauge name{queue=q[, outcome=o]}, 0 if absent.
func metric(t *testing.T, name, queue, outcome string) float64 {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if got["queue"] != queue || got["outcome"] != outcome {
				continue
			}
			switch {
			case m.Counter != nil:
				return m.GetCounter().GetValue()
			case m.Gauge != nil:
				return m.GetGauge().GetValue()
			case m.Histogram != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return 0
}

func TestHistory(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	hist := e.a.HistorySources()["matchmaking"]
	if hist == nil {
		t.Fatal("no matchmaking history")
	}
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	if got, _ := hist(ctx, 0, 0, 10); len(got) != 0 {
		t.Fatalf("a running match is not history: %+v", got)
	}
	if err := e.s.Finish(ctx, m.ID, wins(m, b)); err != nil {
		t.Fatal(err)
	}
	// a free match, finished: never listed
	c, d := e.a.User("carol"), e.a.User("dave")
	e.a.Transport.SetOnline(c, d)
	for i, u := range []int64{c, d} {
		if _, cerr := queue(e, u, duel("f"+string(rune('a'+i)))); cerr != nil {
			t.Fatal(cerr)
		}
	}
	matchNow(t, e)
	free := statusOf(t, e, c).Matches
	if len(free) != 1 {
		t.Fatalf("free match %+v", free)
	}
	if err := e.s.Finish(ctx, free[0].ID, wins(free[0], c)); err != nil {
		t.Fatal(err)
	}

	got, err := hist(ctx, 0, 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("history %+v, %v", got, err)
	}
	by := map[int64]app.Activity{}
	for _, x := range got {
		by[x.UserID] = x
	}
	if w := by[b]; w.Game != "matchmaking" || w.ID != m.ID || w.Amount != 100 || w.Payout != 190 || w.Status != "settled" ||
		w.Currency != "usd" || string(w.Detail) != `{"queue":"bet","result":"won"}` {
		t.Fatalf("winner %+v %s", w, w.Detail)
	}
	if l := by[a]; l.Amount != 100 || l.Payout != 0 || string(l.Detail) != `{"queue":"bet","result":"lost"}` {
		t.Fatalf("loser %+v %s", l, l.Detail)
	}
	if got, _ := hist(ctx, a, 0, 10); len(got) != 1 || got[0].UserID != a {
		t.Fatalf("alice's history %+v", got)
	}
	if got, _ := hist(ctx, 0, m.ID, 10); len(got) != 0 {
		t.Fatalf("before the only match: %+v", got)
	}
}

func TestHistoryCancelled(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b := e.a.User("alice"), e.a.User("bob")
	m := start(t, e, stakedReq("bet", 100), a, b)
	cancelled := metric(t, "gamify_matchmaking_matches_total", "bet", "cancelled")
	if err := e.s.Cancel(ctx, m.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if metric(t, "gamify_matchmaking_matches_total", "bet", "cancelled") != cancelled+1 {
		t.Fatal("cancelled not counted")
	}
	got, err := e.a.HistorySources()["matchmaking"](ctx, a, 0, 10)
	if err != nil || len(got) != 1 || got[0].Status != "cancelled" || got[0].Payout != 0 ||
		string(got[0].Detail) != `{"queue":"bet","result":"cancelled"}` {
		t.Fatalf("history %+v, %v", got, err)
	}
}

func TestMetrics(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	formed := metric(t, "gamify_matchmaking_matches_total", "bet", "formed")
	settled := metric(t, "gamify_matchmaking_matches_total", "bet", "settled")
	waits := metric(t, "gamify_matchmaking_wait_seconds", "bet", "")
	a, b := e.a.User("alice"), e.a.User("bob")
	e.a.Transport.SetOnline(a, b)
	e.a.Fund(a, "usd", 1000)
	e.a.Fund(b, "usd", 1000)
	if _, cerr := queue(e, a, stakedReq("bet", 100)); cerr != nil {
		t.Fatal(cerr)
	}
	e.s.TickNow(ctx) // one waiting
	if got := metric(t, "gamify_matchmaking_tickets", "bet", ""); got != 1 {
		t.Fatalf("tickets gauge %v", got)
	}
	if _, cerr := queue(e, b, stakedReq("bet", 100)); cerr != nil {
		t.Fatal(cerr)
	}
	e.s.TickNow(ctx)
	if got := metric(t, "gamify_matchmaking_tickets", "bet", ""); got != 0 {
		t.Fatalf("tickets gauge %v", got)
	}
	m := onlyMatch(t, e)
	if metric(t, "gamify_matchmaking_matches_total", "bet", "formed") != formed+1 ||
		metric(t, "gamify_matchmaking_wait_seconds", "bet", "") != waits+2 {
		t.Fatal("formed / wait not recorded")
	}
	if err := e.s.Finish(ctx, m.ID, wins(m, a)); err != nil {
		t.Fatal(err)
	}
	if metric(t, "gamify_matchmaking_matches_total", "bet", "settled") != settled+1 {
		t.Fatal("settled not counted")
	}
}
