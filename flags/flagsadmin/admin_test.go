package flagsadmin_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/flags"
	"github.com/amir1430/gamify/flags/flagsadmin"
	"github.com/amir1430/gamify/internal/admintest"
)

func TestFlagsConsole(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	s, err := flags.Register(ctx, a.App, flags.Config{})
	if err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admintest.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := flagsadmin.Register(adm, s); err != nil {
		t.Fatal(err)
	}
	cons := admintest.Start(t, a, adm)
	op, viewer := cons.Login(admin.RoleOperator), cons.Login(admin.RoleViewer)
	alice := a.User("alice")
	get := func() string {
		t.Helper()
		out, cerr := a.Call(alice, "flags.get", nil)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return string(out)
	}
	in := func(input map[string]any) map[string]any { return map[string]any{"input": input} }

	if st, _ := viewer.Do("POST", "/api/views/flags/actions/new", in(map[string]any{"key": "new_lobby"})); st != 403 {
		t.Fatalf("viewer new: %d", st)
	}
	if st, b := op.Do("POST", "/api/views/flags/actions/new", in(map[string]any{"key": "New Lobby"})); st != 400 {
		t.Fatalf("bad key: %d %v", st, b)
	}
	if st, b := op.Do("POST", "/api/views/flags/actions/new", in(map[string]any{"key": "new_lobby", "enabled": true, "percent": 10})); st != 204 {
		t.Fatalf("new: %d %v", st, b)
	}
	if st, b := op.Do("POST", "/api/views/flags/actions/new", in(map[string]any{"key": "new_lobby"})); st != 400 {
		t.Fatalf("duplicate: %d %v", st, b)
	}
	st, body := viewer.Do("GET", "/api/views/flags", nil)
	items, _ := body["items"].([]any)
	if st != 200 || len(items) != 1 {
		t.Fatalf("list: %d %v", st, body)
	}
	row := items[0].(map[string]any)
	if row["key"] != "new_lobby" || row["percent"] != float64(10) || row["testers"] != float64(0) || row["enabled"] != true {
		t.Fatalf("row: %v", row)
	}
	id := strconv.FormatInt(int64(row["id"].(float64)), 10)
	path := "/api/views/flags/" + id + "/actions/"

	aliceID := strconv.FormatInt(alice, 10)
	if st, b := op.Do("POST", path+"testers", in(map[string]any{"tester_ids": []any{aliceID}})); st != 204 {
		t.Fatalf("testers: %d %v", st, b)
	}
	if got := get(); got != `{"flags":{"new_lobby":true}}` {
		t.Fatalf("tester: %s", got)
	}
	for _, bad := range []string{"abc", "999999"} {
		if st, b := op.Do("POST", path+"testers", in(map[string]any{"tester_ids": []any{bad}})); st != 400 {
			t.Fatalf("tester %q: %d %v", bad, st, b)
		}
	}
	st, body = viewer.Do("GET", "/api/views/flags/"+id, nil)
	if st != 200 || fmt.Sprint(body["tester_ids"]) != "["+aliceID+"]" || body["testers"] != float64(1) {
		t.Fatalf("detail: %d %v", st, body)
	}

	if st, b := op.Do("POST", path+"edit", in(map[string]any{"enabled": false, "percent": 10})); st != 204 {
		t.Fatalf("edit: %d %v", st, b)
	}
	if got := get(); got != `{"flags":{"new_lobby":false}}` {
		t.Fatalf("off: %s", got)
	}
	if st, b := op.Do("POST", path+"edit", in(map[string]any{"enabled": true, "percent": 101})); st != 400 {
		t.Fatalf("percent 101: %d %v", st, b)
	}

	if st, b := op.Do("POST", path+"delete", nil); st != 204 {
		t.Fatalf("delete: %d %v", st, b)
	}
	if got := get(); got != `{"flags":{}}` {
		t.Fatalf("deleted: %s", got)
	}
	if st, _ := viewer.Do("GET", "/api/views/flags/"+id, nil); st != 404 {
		t.Fatalf("deleted detail: %d", st)
	}
	if st, b := op.Do("POST", path+"edit", in(map[string]any{"enabled": true, "percent": 1})); st != 409 && st != 404 {
		t.Fatalf("edit deleted: %d %v", st, b)
	}

	for _, act := range []string{"new", "testers", "edit", "delete"} {
		var n int
		if err := a.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE action = $1`, "view.flags."+act).Scan(&n); err != nil || n == 0 {
			t.Errorf("audit %s: %d %v", act, n, err)
		}
	}
	var key, target string
	if err := a.Pool.QueryRow(ctx, `SELECT before->>'key', target FROM admin_audit WHERE action = 'view.flags.delete'`).Scan(&key, &target); err != nil || key != "new_lobby" || target != "flags:"+id {
		t.Fatalf("delete audit: %q %q %v", key, target, err)
	}
}
