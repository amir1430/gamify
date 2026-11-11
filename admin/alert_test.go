package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func countOf(n int64) func(context.Context) (int64, error) {
	return func(context.Context) (int64, error) { return n, nil }
}

func TestAlertValidation(t *testing.T) {
	a, adm, _ := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	ok := Alert{Name: "open-things", Title: "Open things", Perm: PermView, Count: countOf(1),
		View: "things", Filters: map[string]string{"state": "open"}}
	for name, tc := range map[string]struct {
		edit func(*Alert)
		want string
	}{
		"bad name":        {func(x *Alert) { x.Name = "Bad Name" }, "name"},
		"no title":        {func(x *Alert) { x.Title = "" }, "title"},
		"unknown perm":    {func(x *Alert) { x.Perm = "nope" }, "perm"},
		"no count":        {func(x *Alert) { x.Count = nil }, "count"},
		"bad tone":        {func(x *Alert) { x.Tone = "pink" }, "tone"},
		"unknown view":    {func(x *Alert) { x.View = "ghosts" }, "view"},
		"filters no view": {func(x *Alert) { x.View = "" }, "filters"},
		"unknown filter":  {func(x *Alert) { x.Filters = map[string]string{"colour": "red"} }, "filter"},
		"bad option":      {func(x *Alert) { x.Filters = map[string]string{"state": "lost"} }, "filter"},
	} {
		x := ok
		tc.edit(&x)
		if err := adm.Alert(x); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one about %q", name, err, tc.want)
		}
	}
	if err := adm.Alert(ok); err != nil {
		t.Fatal(err)
	}
	if err := adm.Alert(ok); err == nil {
		t.Fatal("a duplicate name was accepted")
	}
}

func TestAlerts(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	for _, x := range []Alert{
		{Name: "open-things", Title: "Open things", Hint: "waiting", Perm: PermView, Count: countOf(3),
			View: "things", Filters: map[string]string{"state": "open"}},
		{Name: "closing", Title: "To close", Perm: "things.close", Tone: AlertDanger, Count: countOf(0)},
		{Name: "broken", Title: "Broken", Perm: PermView, Count: func(context.Context) (int64, error) { return 0, errors.New("db down") }},
	} {
		if err := adm.Alert(x); err != nil {
			t.Fatal(err)
		}
	}
	alerts := func(c *http.Client) map[string]map[string]any {
		t.Helper()
		st, body := call(t, c, srv, "GET", "/api/alerts", nil)
		if st != 200 {
			t.Fatalf("alerts: %d %v", st, body)
		}
		out := map[string]map[string]any{}
		for _, it := range body["items"].([]any) {
			m := it.(map[string]any)
			out[m["name"].(string)] = m
		}
		return out
	}

	viewer := alerts(loginAs(t, a, srv, "viewer", RoleViewer))
	if len(viewer) != 2 || viewer["closing"] != nil {
		t.Fatalf("viewer sees %v, want open-things and broken only", viewer)
	}
	o := viewer["open-things"]
	if o["count"] != 3.0 || o["title"] != "Open things" || o["hint"] != "waiting" || o["tone"] != "warn" ||
		o["view"] != "things" || o["filters"].(map[string]any)["state"] != "open" || o["error"] != nil {
		t.Fatalf("open-things = %v", o)
	}
	// A failing count is the alert's error, not the page's 500; never the error's text (it may hold internals).
	if b := viewer["broken"]; b["count"] != nil || b["error"] != "unavailable" {
		t.Fatalf("broken = %v", b)
	}
	support := alerts(loginAs(t, a, srv, "support", RoleSupport))
	if c := support["closing"]; c == nil || c["count"] != 0.0 || c["tone"] != "danger" {
		t.Fatalf("support's closing = %v", c)
	}
}

func TestSystemStatusAndFlags(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	cols := []Column{{Key: "name", Type: Text}, {Key: "up", Type: Bool}}
	rows := func(context.Context) ([]Row, error) { return []Row{{"name": "w1", "up": true}}, nil }
	for name, s := range map[string]Status{
		"bad name":     {Name: "X", Title: "x", Perm: PermView, Columns: cols, Rows: rows},
		"no title":     {Name: "x", Perm: PermView, Columns: cols, Rows: rows},
		"no columns":   {Name: "x", Title: "x", Perm: PermView, Rows: rows},
		"no rows":      {Name: "x", Title: "x", Perm: PermView, Columns: cols},
		"bad perm":     {Name: "x", Title: "x", Perm: "nope", Columns: cols, Rows: rows},
		"money no cur": {Name: "x", Title: "x", Perm: PermView, Columns: []Column{{Key: "m", Type: Money}}, Rows: rows},
	} {
		if err := adm.Status(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := adm.Status(Status{Name: "workers", Title: "Workers", Perm: PermView, Columns: cols, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	if err := adm.Status(Status{Name: "secret", Title: "Secret", Perm: "things.close", Columns: cols, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	if err := adm.Status(Status{Name: "workers", Title: "Again", Perm: PermView, Columns: cols, Rows: rows}); err == nil {
		t.Fatal("a duplicate status was accepted")
	}

	st, body := call(t, loginAs(t, a, srv, "viewer", RoleViewer), srv, "GET", "/api/system", nil)
	if st != 200 {
		t.Fatalf("system: %d %v", st, body)
	}
	secs := body["sections"].([]any)
	if len(secs) != 1 {
		t.Fatalf("viewer sections = %v, want workers only", secs)
	}
	w := secs[0].(map[string]any)
	if w["name"] != "workers" || w["title"] != "Workers" || len(w["columns"].([]any)) != 2 ||
		w["items"].([]any)[0].(map[string]any)["name"] != "w1" {
		t.Fatalf("workers = %v", w)
	}
	for _, c := range body["currencies"].([]any) {
		m := c.(map[string]any)
		if _, ok := m["transferable"].(bool); !ok {
			t.Fatalf("currency %v: no transferable", m)
		}
		if _, ok := m["withdrawable"].(bool); !ok {
			t.Fatalf("currency %v: no withdrawable", m)
		}
	}
}

func TestViewAuditActions(t *testing.T) {
	a, adm, srv := thingsEnv(t, thingsPerms)
	if err := adm.View(thingsView(a)); err != nil {
		t.Fatal(err)
	}
	// The viewer can't run close but may filter the audit log by it.
	st, views := getList(t, loginAs(t, a, srv, "viewer", RoleViewer), srv, "/api/views")
	if st != 200 {
		t.Fatalf("views: %d", st)
	}
	got := views[0]["audit_actions"].([]any)
	if len(got) != 1 || got[0] != "view.things.close" {
		t.Fatalf("audit_actions = %v", got)
	}
}
