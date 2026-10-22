package matchmakingadmin_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/internal/admintest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"
	"github.com/amir1430/gamify/matchmaking/matchmakingadmin"
	"github.com/amir1430/gamify/party"
)

type env struct {
	a    *apptest.App
	s    *matchmaking.Service
	op   *admintest.Client
	cons *admintest.Console
	mid  int64 // the running staked match
	u    [2]int64
}

func setup(t *testing.T) env {
	t.Helper()
	a, ctx := apptest.New(t), context.Background()
	p, err := party.Register(ctx, a.App, party.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := matchmaking.Register(ctx, a.App, p, matchmaking.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queue(matchmaking.QueueConfig{Name: "bet", Ranked: true, Teams: []matchmaking.Team{{Min: 1, Max: 1}, {Min: 1, Max: 1}},
		Game: a.Games.PvPGame("arena", 1000, 1<<40, nil), Stakes: map[ledger.Currency][]int64{"usd": {100}}}); err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admintest.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := matchmakingadmin.Register(adm, s); err != nil {
		t.Fatal(err)
	}
	e := env{a: a, s: s, cons: admintest.Start(t, a, adm)}
	e.op = e.cons.Login(admin.RoleOperator)
	e.u = [2]int64{a.User("alice"), a.User("bob")}
	a.Transport.SetOnline(e.u[0], e.u[1])
	for i, u := range e.u {
		a.Fund(u, "usd", 1000)
		if _, cerr := apptest.Call[matchmaking.QueueResult](a, u, "match.queue",
			map[string]any{"queue": "bet", "currency": "usd", "stake": 100, "idem_key": "k" + strconv.Itoa(i)}); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if err := s.MatchNow(ctx); err != nil {
		t.Fatal(err)
	}
	ms, err := s.Matches(ctx, matchmaking.MatchFilter{}, 0, 10)
	if err != nil || len(ms) != 1 || ms[0].Status != "running" {
		t.Fatalf("matches %+v %v", ms, err)
	}
	e.mid = ms[0].ID
	return e
}

func (e env) action(t *testing.T, name string, body any) (int, map[string]any) {
	t.Helper()
	return e.op.Do("POST", "/api/views/mm-matches/"+strconv.FormatInt(e.mid, 10)+"/actions/"+name, body)
}

func (e env) status(t *testing.T) string {
	t.Helper()
	m, err := e.s.Get(context.Background(), e.mid)
	if err != nil {
		t.Fatal(err)
	}
	return m.Status
}

// auditReason is the reason column of the action's one audit row.
func auditReason(t *testing.T, e env, action string) (reason string) {
	t.Helper()
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT reason FROM admin_audit WHERE action = $1`, action).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	return
}

func audits(t *testing.T, e env, action string) (n int) {
	t.Helper()
	if err := e.a.Pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

func TestCancel(t *testing.T) {
	e := setup(t)
	if st, body := e.action(t, "cancel", map[string]any{}); st != 400 { // reason is required
		t.Fatalf("no reason: %d %v", st, body)
	}
	if st, body := e.action(t, "cancel", map[string]any{"reason": "cheating"}); st != 204 {
		t.Fatalf("cancel: %d %v", st, body)
	}
	if got := e.status(t); got != "cancelling" {
		t.Fatalf("status %s, want cancelling", got)
	}
	if b := e.a.Balance(e.u[0], "usd"); b != 900 {
		t.Fatalf("balance %d before the reconciler", b)
	}
	if audits(t, e, "view.mm-matches.cancel") != 1 || auditReason(t, e, "view.mm-matches.cancel") != "cheating" {
		t.Fatal("no audit row with the reason")
	}
	if st, body := e.action(t, "cancel", map[string]any{"reason": "again"}); st != 204 { // a repeat is a no-op
		t.Fatalf("repeat: %d %v", st, body)
	}
	e.a.ReconcileNow()
	if got := e.status(t); got != "cancelled" {
		t.Fatalf("status %s, want cancelled", got)
	}
	for _, u := range e.u {
		if b := e.a.Balance(u, "usd"); b != 1000 {
			t.Fatalf("user %d balance %d, want the refund", u, b)
		}
	}
}

func TestReport(t *testing.T) {
	e := setup(t)
	for _, bad := range []string{"", "x", "1", "0,1", "1,2,3"} {
		if st, body := e.action(t, "report", map[string]any{"input": map[string]any{"ranks": bad}, "reason": "r"}); st != 400 {
			t.Fatalf("ranks %q: %d %v, want 400", bad, st, body)
		}
	}
	if got := e.status(t); got != "running" || audits(t, e, "view.mm-matches.report") != 0 {
		t.Fatalf("a refused report changed %s", got)
	}
	ms, _ := e.s.Matches(context.Background(), matchmaking.MatchFilter{}, 0, 1)
	// team 0 loses, team 1 wins: "2,1"
	if st, body := e.action(t, "report", map[string]any{"input": map[string]any{"ranks": "2,1"}, "reason": "ref said"}); st != 204 {
		t.Fatalf("report: %d %v", st, body)
	}
	if got := e.status(t); got != "finishing" || audits(t, e, "view.mm-matches.report") != 1 {
		t.Fatalf("status %s", got)
	}
	if auditReason(t, e, "view.mm-matches.report") != "ref said" {
		t.Fatal("audit reason")
	}
	if st, body := e.action(t, "report", map[string]any{"input": map[string]any{"ranks": "1,2"}, "reason": "x"}); st != 409 {
		t.Fatalf("conflicting result: %d %v", st, body)
	}
	e.a.ReconcileNow()
	if got := e.status(t); got != "settled" {
		t.Fatalf("status %s, want settled", got)
	}
	winner := ms[0].Players[0].UserID
	for _, p := range ms[0].Players {
		if p.Team == 1 {
			winner = p.UserID
		}
	}
	if b := e.a.Balance(winner, "usd"); b != 1090 {
		t.Fatalf("winner balance %d, want 1090", b)
	}
}

func TestReportForming(t *testing.T) {
	e := setup(t)
	if _, err := e.a.Pool.Exec(context.Background(), `UPDATE mm_matches SET status = 'forming' WHERE id = $1`, e.mid); err != nil {
		t.Fatal(err)
	}
	if st, body := e.action(t, "report", map[string]any{"input": map[string]any{"ranks": "2,1"}, "reason": "r"}); st != 409 {
		t.Fatalf("report forming: %d %v, want 409", st, body)
	}
}

func TestViews(t *testing.T) {
	e := setup(t)
	viewer := e.cons.Login(admin.RoleViewer)
	if st, _ := viewer.Do("POST", "/api/views/mm-matches/"+strconv.FormatInt(e.mid, 10)+"/actions/cancel",
		map[string]any{"reason": "x"}); st != 403 {
		t.Fatalf("viewer cancel: %d", st)
	}
	st, body := e.op.Do("GET", "/api/views/mm-matches?user_id="+strconv.FormatInt(e.u[0], 10)+"&queue=bet&status=running", nil)
	items, _ := body["items"].([]any)
	if st != 200 || len(items) != 1 {
		t.Fatalf("matches: %d %v", st, body)
	}
	row := items[0].(map[string]any)
	if row["stake"] != 100.0 || row["pot"] != 200.0 || row["fee"] != float64(ledger.Fee(200)) || row["queue"] != "bet" {
		t.Fatalf("row %v", row)
	}
	if st, body := e.op.Do("GET", "/api/views/mm-matches?status=settled", nil); st != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("settled: %d %v", st, body)
	}
	if st, body := e.op.Do("GET", "/api/views/mm-matches/"+strconv.FormatInt(e.mid, 10), nil); st != 200 || body["status"] != "running" {
		t.Fatalf("detail: %d %v", st, body)
	}

	// one more player queues: mm-queues shows the waiting ticket
	c := e.a.User("carol")
	e.a.Transport.SetOnline(c)
	e.a.Fund(c, "usd", 1000)
	if _, cerr := apptest.Call[matchmaking.QueueResult](e.a, c, "match.queue",
		map[string]any{"queue": "bet", "currency": "usd", "stake": 100, "idem_key": "kc"}); cerr != nil {
		t.Fatal(cerr)
	}
	st, body = e.op.Do("GET", "/api/views/mm-queues", nil)
	items, _ = body["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["name"] != "bet" || items[0].(map[string]any)["waiting"] != 1.0 {
		t.Fatalf("queues: %d %v", st, body)
	}

	// finish the match: ratings appear
	if err := e.s.Finish(context.Background(), e.mid, matchmaking.Result{Ranks: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	st, body = e.op.Do("GET", "/api/views/mm-ratings?user_id="+strconv.FormatInt(e.u[0], 10), nil)
	items, _ = body["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["key"] != "bet" || items[0].(map[string]any)["matches"] != 1.0 || items[0].(map[string]any)["id"] == nil {
		t.Fatalf("ratings: %d %v", st, body)
	}
}

func TestRefusals(t *testing.T) {
	e := setup(t)
	do := func(id int64, name string, body any) int {
		st, _ := e.op.Do("POST", "/api/views/mm-matches/"+strconv.FormatInt(id, 10)+"/actions/"+name, body)
		return st
	}
	if st := do(9999, "cancel", map[string]any{"reason": "x"}); st != 404 {
		t.Fatalf("unknown match: %d", st)
	}
	if st := do(e.mid, "cancel", map[string]any{"reason": "  "}); st != 400 {
		t.Fatalf("blank reason: %d", st)
	}
	if err := e.s.Finish(context.Background(), e.mid, matchmaking.Result{Ranks: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if st := do(e.mid, "cancel", map[string]any{"reason": "late"}); st != 409 { // settled: ErrClosed
		t.Fatalf("cancel settled: %d", st)
	}
}
