package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/amir1430/gamify/apptest"

	"github.com/jackc/pgx/v5"
)

// thingsEnv is an admin with a test table things and, from opts, Config changes.
func thingsEnv(t *testing.T, opts ...func(*Config)) (*apptest.App, *Admin, *httptest.Server) {
	t.Helper()
	a, adm, srv := newAdmin(t, opts...)
	if _, err := a.Pool.Exec(context.Background(),
		`CREATE TABLE things (id bigserial PRIMARY KEY, user_id bigint, amount bigint, currency text, state text)`); err != nil {
		t.Fatal(err)
	}
	return a, adm, srv
}

func addThing(t *testing.T, a *apptest.App, userID, amount int64, state string) int64 {
	t.Helper()
	var id int64
	if err := a.Pool.QueryRow(context.Background(),
		`INSERT INTO things (user_id, amount, currency, state) VALUES ($1, $2, 'usd', $3) RETURNING id`,
		userID, amount, state).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func thingState(t *testing.T, a *apptest.App, id int64) string {
	t.Helper()
	var s string
	if err := a.Pool.QueryRow(context.Background(), `SELECT state FROM things WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func listThings(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, q Query) (Page, error) {
	sql := `SELECT id, user_id, amount, currency, state FROM things WHERE ($1::bigint = 0 OR id < $1)`
	args := []any{q.Before}
	if v, ok := q.Filters["user_id"]; ok {
		n, _ := strconv.ParseInt(v, 10, 64)
		args = append(args, n)
		sql += ` AND user_id = $` + strconv.Itoa(len(args))
	}
	if v, ok := q.Filters["state"]; ok {
		args = append(args, v)
		sql += ` AND state = $` + strconv.Itoa(len(args))
	}
	args = append(args, q.Limit)
	sql += ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var p Page
	for rows.Next() {
		var id, user, amount int64
		var cur, state string
		if err := rows.Scan(&id, &user, &amount, &cur, &state); err != nil {
			return Page{}, err
		}
		p.Items = append(p.Items, Row{"id": id, "user_id": user, "amount": amount, "currency": cur, "state": state})
	}
	if len(p.Items) == int(q.Limit) {
		n := p.Items[len(p.Items)-1]["id"].(int64)
		p.Next = &n
	}
	return p, rows.Err()
}

// closeRun sets a thing's state to closed in tx.
func closeRun(ctx context.Context, tx pgx.Tx, c Call) (any, any, error) {
	var before string
	if err := tx.QueryRow(ctx, `SELECT state FROM things WHERE id = $1`, c.ID).Scan(&before); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE things SET state = 'closed' WHERE id = $1`, c.ID); err != nil {
		return nil, nil, err
	}
	return map[string]string{"state": before}, map[string]string{"state": "closed"}, nil
}

// thingsView is a valid view over things; a is its database.
func thingsView(a *apptest.App) View {
	return View{
		Name: "things", Title: "Things", Group: "Test", Perm: PermView, UserFilter: "user_id",
		Columns: []Column{
			{Key: "id", Type: Int},
			{Key: "user_id", Type: UserID},
			{Key: "amount", Type: Money, CurrencyKey: "currency"},
			{Key: "state", Type: Enum, Options: []string{"open", "closed"}},
		},
		Filters: []Filter{{Key: "user_id", Type: UserID}, {Key: "state", Type: Enum, Options: []string{"open", "closed"}}},
		List:    func(ctx context.Context, q Query) (Page, error) { return listThings(ctx, a.Pool, q) },
		Get: func(ctx context.Context, id int64) (Row, error) {
			var user, amount int64
			var cur, state string
			err := a.Pool.QueryRow(ctx, `SELECT user_id, amount, currency, state FROM things WHERE id = $1`, id).Scan(&user, &amount, &cur, &state)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return Row{"id": id, "user_id": user, "amount": amount, "currency": cur, "state": state}, err
		},
		Actions: []Action{{Name: "close", Title: "Close", Confirm: "Close it?", Perm: "things.close", Reason: true, Run: closeRun}},
	}
}

var thingsPerms = func(c *Config) { c.Perms = map[Perm][]Role{"things.close": {RoleSupport}} }

// getList is a GET whose answer is a JSON array.
func getList(t *testing.T, c *http.Client, srv *httptest.Server, path string) (int, []map[string]any) {
	t.Helper()
	resp, err := c.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if resp.StatusCode == 200 {
		if err := json.Unmarshal(raw, &out); err != nil || out.Items == nil {
			t.Fatalf("GET %s: want {\"items\": [...]}, got %q", path, raw)
		}
	}
	return resp.StatusCode, out.Items
}

func TestViewValidation(t *testing.T) {
	a, adm, _ := thingsEnv(t, thingsPerms)
	for name, c := range map[string]struct {
		field string
		f     func(*View)
	}{
		"bad name":             {"name", func(v *View) { v.Name = "Things!" }},
		"long name":            {"name", func(v *View) { v.Name = strings.Repeat("a", 41) }},
		"no title":             {"title", func(v *View) { v.Title = "" }},
		"unknown perm":         {"perm", func(v *View) { v.Perm = "nope" }},
		"no perm":              {"perm", func(v *View) { v.Perm = "" }},
		"no columns":           {"columns", func(v *View) { v.Columns = nil; v.Filters = nil; v.UserFilter = "" }},
		"duplicate column":     {"id", func(v *View) { v.Columns = append(v.Columns, Column{Key: "id", Type: Int}) }},
		"unknown type":         {"x", func(v *View) { v.Columns = append(v.Columns, Column{Key: "x", Type: "float"}) }},
		"money no currency":    {"amount", func(v *View) { v.Columns[2].CurrencyKey = "" }},
		"money both":           {"amount", func(v *View) { v.Columns[2].Currency = "usd" }},
		"decimal scale 0":      {"x", func(v *View) { v.Columns = append(v.Columns, Column{Key: "x", Type: Decimal}) }},
		"decimal scale 19":     {"x", func(v *View) { v.Columns = append(v.Columns, Column{Key: "x", Type: Decimal, Scale: 19}) }},
		"enum no options":      {"state", func(v *View) { v.Columns[3].Options = nil; v.Filters[1].Options = nil }},
		"filter not a column":  {"nope", func(v *View) { v.Filters = append(v.Filters, Filter{Key: "nope", Type: Int}) }},
		"filter type differs":  {"user_id", func(v *View) { v.Filters[0].Type = Text }},
		"user filter missing":  {"user_filter", func(v *View) { v.UserFilter = "amount" }},
		"user filter not int":  {"user_filter", func(v *View) { v.UserFilter = "state" }},
		"nil list":             {"list", func(v *View) { v.List = nil }},
		"bad action name":      {"action", func(v *View) { v.Actions[0].Name = "Close" }},
		"duplicate action":     {"close", func(v *View) { v.Actions = append(v.Actions, v.Actions[0]) }},
		"nil run":              {"close", func(v *View) { v.Actions[0].Run = nil }},
		"action unknown perm":  {"close", func(v *View) { v.Actions[0].Perm = "things.open" }},
		"field enum no option": {"kind", func(v *View) { v.Actions[0].Fields = []Field{{Key: "kind", Type: Enum}} }},
	} {
		v := thingsView(a)
		c.f(&v)
		err := adm.View(v)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: %v doesn't name %q", name, err, c.field)
		}
	}
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	if err := adm.View(thingsView(a)); err == nil || !strings.Contains(err.Error(), "things") {
		t.Fatalf("second View = %v, want duplicate", err)
	}

	// Config.Perms is checked by Register.
	for name, perms := range map[string]map[Perm][]Role{
		"built-in":     {PermView: {RoleSupport}},
		"empty":        {"": {RoleSupport}},
		"unknown role": {"things.close": {"root"}},
	} {
		_, err := Register(context.Background(), apptest.New(t).App,
			Config{Addr: ":0", Origin: testOrigin, TOTPKey: testKey, Perms: perms})
		if err == nil {
			t.Errorf("Config.Perms %s: accepted", name)
		}
	}
}

func TestViewPerms(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	support := loginAs(t, a, srv, "support", RoleSupport)
	op := loginAs(t, a, srv, "op", RoleOperator)
	owner := loginAs(t, a, srv, "owner", RoleOwner)

	actions := func(c *http.Client) []any {
		t.Helper()
		st, views := getList(t, c, srv, "/api/views")
		if st != 200 || len(views) != 1 || views[0]["name"] != "things" || views[0]["user_filter"] != "user_id" ||
			views[0]["has_detail"] != true {
			t.Fatalf("views: %d %v", st, views)
		}
		return views[0]["actions"].([]any)
	}
	if acts := actions(viewer); len(acts) != 0 {
		t.Fatalf("viewer sees %v", acts)
	}
	if acts := actions(op); len(acts) != 0 {
		t.Fatalf("operator sees %v", acts)
	}
	if acts := actions(support); len(acts) != 1 || acts[0].(map[string]any)["name"] != "close" ||
		acts[0].(map[string]any)["reason"] != true {
		t.Fatalf("support sees %v", acts)
	}

	id := addThing(t, a, 1, 10, "open")
	path := "/api/views/things/" + strconv.FormatInt(id, 10) + "/actions/close"
	st, body := call(t, viewer, srv, "POST", path, map[string]any{"reason": "r"})
	wantErr(t, "viewer close", st, body, 403, "forbidden")
	st, body = call(t, op, srv, "POST", path, map[string]any{"reason": "r"})
	wantErr(t, "operator close", st, body, 403, "forbidden")
	if thingState(t, a, id) != "open" {
		t.Fatal("refused action ran")
	}
	if st, body := call(t, support, srv, "POST", path, map[string]any{"reason": "r"}); st != 204 {
		t.Fatalf("support close: %d %v", st, body)
	}
	id2 := addThing(t, a, 1, 10, "open")
	if st, body := call(t, owner, srv, "POST", "/api/views/things/"+strconv.FormatInt(id2, 10)+"/actions/close",
		map[string]any{"reason": "r"}); st != 204 {
		t.Fatalf("owner close: %d %v", st, body)
	}

	// /api/me lists the brand perm for support and owner, not for viewer.
	for c, want := range map[*http.Client]bool{viewer: false, support: true, owner: true} {
		_, me := call(t, c, srv, "GET", "/api/me", nil)
		has := false
		for _, p := range me["permissions"].([]any) {
			has = has || p == "things.close"
		}
		if has != want {
			t.Errorf("/api/me permissions %v, want things.close %v", me["permissions"], want)
		}
	}

	// A view the role can't see: hidden and refused.
	v := thingsView(a)
	v.Name, v.Perm, v.Actions = "secret", PermAdmins, nil
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	if st, views := getList(t, support, srv, "/api/views"); st != 200 || len(views) != 1 {
		t.Fatalf("support sees %v", views)
	}
	st, body = call(t, support, srv, "GET", "/api/views/secret", nil)
	wantErr(t, "support list secret", st, body, 403, "forbidden")
	st, body = call(t, support, srv, "GET", "/api/views/secret/"+strconv.FormatInt(id, 10), nil)
	wantErr(t, "support get secret", st, body, 403, "forbidden")
	if st, _ := call(t, owner, srv, "GET", "/api/views/secret", nil); st != 200 {
		t.Fatalf("owner list secret: %d", st)
	}
	st, body = call(t, owner, srv, "GET", "/api/views/nope", nil)
	wantErr(t, "unknown view", st, body, 404, "not_found")
}

func TestViewActionAudited(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	support := loginAs(t, a, srv, "support", RoleSupport)
	id := addThing(t, a, 1, 10, "open")
	path := "/api/views/things/" + strconv.FormatInt(id, 10) + "/actions/close"

	st, body := call(t, support, srv, "POST", path, map[string]any{"reason": "  "})
	wantErr(t, "blank reason", st, body, 400, "reason_required")
	st, body = call(t, support, srv, "POST", path, map[string]any{})
	wantErr(t, "no reason", st, body, 400, "reason_required")
	if thingState(t, a, id) != "open" || auditRows(t, a, "view.things.close") != 0 {
		t.Fatal("action without a reason changed something")
	}

	if st, body := call(t, support, srv, "POST", path, map[string]any{"reason": "customer asked"}); st != 204 {
		t.Fatalf("close: %d %v", st, body)
	}
	if thingState(t, a, id) != "closed" {
		t.Fatal("row not changed")
	}
	var target, reason string
	var before, after []byte
	if err := a.Pool.QueryRow(context.Background(),
		`SELECT target, reason, before, after FROM admin_audit WHERE action = 'view.things.close'`).Scan(&target, &reason, &before, &after); err != nil {
		t.Fatal(err)
	}
	if target != "things:"+strconv.FormatInt(id, 10) || reason != "customer asked" ||
		!strings.Contains(string(before), `"open"`) || !strings.Contains(string(after), `"closed"`) {
		t.Fatalf("audit row: %s %s %s %s", target, reason, before, after)
	}
	if n := auditRows(t, a, "view.things.close"); n != 1 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestViewActionInput(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	var got map[string]any
	v := thingsView(a)
	v.Actions = []Action{{Name: "set", Title: "Set", Perm: PermView, Fields: []Field{
		{Key: "n", Type: Int, Required: true},
		{Key: "amount", Type: Money, Currency: "usd"},
		{Key: "on", Type: Bool},
		{Key: "kind", Type: Enum, Options: []string{"a", "b"}},
		{Key: "note", Type: Text},
		{Key: "at", Type: Time},
	}, Run: func(ctx context.Context, tx pgx.Tx, c Call) (any, any, error) {
		got = c.Input
		return nil, nil, nil
	}}}
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	path := "/api/views/things/1/actions/set"
	for name, in := range map[string]map[string]any{
		"missing required": {"on": true},
		"unknown key":      {"n": 1, "x": 1},
		"float":            {"n": 1.5},
		"string int":       {"n": "abc"},
		"bad enum":         {"n": 1, "kind": "c"},
		"bad bool":         {"n": 1, "on": "yes"},
		"bad time":         {"n": 1, "at": "yesterday"},
	} {
		st, body := call(t, viewer, srv, "POST", path, map[string]any{"input": in})
		wantErr(t, name, st, body, 400, "bad_input")
	}
	if got != nil {
		t.Fatalf("Run ran with %v", got)
	}
	if st, body := call(t, viewer, srv, "POST", path, map[string]any{"input": map[string]any{
		"n": "1152921504606846976", "amount": 250, "on": true, "kind": "b", "note": "hi", "at": "2026-10-21T10:00:00Z",
	}}); st != 204 {
		t.Fatalf("set: %d %v", st, body)
	}
	if got["n"] != int64(1<<60) || got["amount"] != int64(250) || got["on"] != true || got["kind"] != "b" || got["note"] != "hi" {
		t.Fatalf("input = %#v", got)
	}
}

func TestViewActionRollsBack(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	v := thingsView(a)
	update := func(ctx context.Context, tx pgx.Tx, id int64) {
		if _, err := tx.Exec(ctx, `UPDATE things SET state = 'closed' WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	v.Actions = []Action{
		{Name: "fail", Title: "Fail", Perm: PermView, Run: func(ctx context.Context, tx pgx.Tx, c Call) (any, any, error) {
			update(ctx, tx, c.ID)
			return nil, nil, errors.New("boom")
		}},
		{Name: "panic", Title: "Panic", Perm: PermView, Run: func(ctx context.Context, tx pgx.Tx, c Call) (any, any, error) {
			update(ctx, tx, c.ID)
			panic("boom")
		}},
	}
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	id := addThing(t, a, 1, 10, "open")
	for _, act := range []string{"fail", "panic"} {
		st, body := call(t, viewer, srv, "POST", "/api/views/things/"+strconv.FormatInt(id, 10)+"/actions/"+act, nil)
		wantErr(t, act, st, body, 500, "action_failed")
		if thingState(t, a, id) != "open" || auditRows(t, a, "view.things."+act) != 0 {
			t.Fatalf("%s: change or audit row kept", act)
		}
	}
	if st, body := call(t, viewer, srv, "GET", "/api/views/things", nil); st != 200 {
		t.Fatalf("after a panic: %d %v", st, body)
	}

	// Panics in List and Get.
	v = thingsView(a)
	v.Name, v.Actions = "broken", nil
	v.List = func(context.Context, Query) (Page, error) { panic("list") }
	v.Get = func(context.Context, int64) (Row, error) { panic("get") }
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	st, body := call(t, viewer, srv, "GET", "/api/views/broken", nil)
	wantErr(t, "list panic", st, body, 500, "view_failed")
	st, body = call(t, viewer, srv, "GET", "/api/views/broken/1", nil)
	wantErr(t, "get panic", st, body, 500, "view_failed")
}

func TestViewFilters(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	var got Query
	v := thingsView(a)
	list := v.List
	v.List = func(ctx context.Context, q Query) (Page, error) { got = q; return list(ctx, q) }
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	for _, q := range []string{"user_id=abc", "state=nope", "colour=red", "amount=5"} {
		st, body := call(t, viewer, srv, "GET", "/api/views/things?"+q, nil)
		wantErr(t, q, st, body, 400, "bad_filter")
	}
	a1 := addThing(t, a, 7, 10, "open")
	addThing(t, a, 7, 10, "closed")
	addThing(t, a, 8, 10, "open")
	st, body := call(t, viewer, srv, "GET", "/api/views/things?user_id=7&state=open&limit=500", nil)
	if st != 200 {
		t.Fatalf("list: %d %v", st, body)
	}
	if got.Filters["user_id"] != "7" || got.Filters["state"] != "open" || len(got.Filters) != 2 || got.Limit != 100 || got.Before != 0 {
		t.Fatalf("query = %+v", got)
	}
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != float64(a1) || body["next"] != nil {
		t.Fatalf("page = %v", body)
	}
	if st, body := call(t, viewer, srv, "GET", "/api/views/things?before=5&limit=2", nil); st != 200 || got.Before != 5 || got.Limit != 2 || len(got.Filters) != 0 {
		t.Fatalf("paged: %d %v %+v", st, body, got)
	}

	// Detail.
	st, body = call(t, viewer, srv, "GET", "/api/views/things/"+strconv.FormatInt(a1, 10), nil)
	if st != 200 || body["state"] != "open" {
		t.Fatalf("get: %d %v", st, body)
	}
	st, body = call(t, viewer, srv, "GET", "/api/views/things/999999", nil)
	wantErr(t, "missing row", st, body, 404, "not_found")
}

func TestViewLargeInts(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	big := addThing(t, a, 1, 1<<60, "open")
	small := addThing(t, a, 1, 1<<53, "open")
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	st, body := call(t, viewer, srv, "GET", "/api/views/things", nil)
	if st != 200 {
		t.Fatalf("list: %d %v", st, body)
	}
	amounts := map[float64]any{}
	for _, it := range body["items"].([]any) {
		m := it.(map[string]any)
		amounts[m["id"].(float64)] = m["amount"]
	}
	if amounts[float64(big)] != "1152921504606846976" || amounts[float64(small)] != float64(1<<53) {
		t.Fatalf("amounts = %v", amounts)
	}
}

// After runs once, after the commit, with Run's before and after; never for a failed action.
func TestViewActionAfter(t *testing.T) {
	a, adm, srv := thingsEnv(t)
	v := thingsView(a)
	var got []string
	run := func(fail bool) func(context.Context, pgx.Tx, Call) (any, any, error) {
		return func(ctx context.Context, tx pgx.Tx, c Call) (any, any, error) {
			if fail {
				return nil, nil, errors.New("boom")
			}
			return "was", "now", nil
		}
	}
	after := func(_ context.Context, c Call, before, after any) {
		var state string
		if err := a.Pool.QueryRow(context.Background(), `SELECT count(*)::text FROM admin_audit WHERE action = 'view.things.ok'`).Scan(&state); err != nil {
			t.Error(err)
		}
		got = append(got, state+" "+before.(string)+" "+after.(string))
	}
	v.Actions = []Action{
		{Name: "ok", Title: "Ok", Perm: PermView, Run: run(false), After: after},
		{Name: "bad", Title: "Bad", Perm: PermView, Run: run(true), After: after},
	}
	if err := adm.View(v); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, a, srv, "viewer", RoleViewer)
	id := strconv.FormatInt(addThing(t, a, 1, 10, "open"), 10)
	if st, _ := call(t, viewer, srv, "POST", "/api/views/things/"+id+"/actions/bad", nil); st != 500 {
		t.Fatalf("bad: %d", st)
	}
	if len(got) != 0 {
		t.Fatalf("After ran for a failed action: %v", got)
	}
	if st, body := call(t, viewer, srv, "POST", "/api/views/things/"+id+"/actions/ok", nil); st != 204 {
		t.Fatalf("ok: %d %v", st, body)
	}
	if len(got) != 1 || got[0] != "1 was now" { // the audit row was committed when After ran
		t.Fatalf("After calls: %v", got)
	}
}

func TestMatchmakingPerms(t *testing.T) {
	for _, p := range []Perm{PermMatchCancel, PermMatchReport, PermPartyDisband} {
		if !builtin(p) || !RoleOwner.Can(p) || !RoleOperator.Can(p) || RoleSupport.Can(p) || RoleViewer.Can(p) {
			t.Fatalf("perm %s: owner and operator only", p)
		}
	}
}
