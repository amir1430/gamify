package restrictadmin_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/internal/admintest"
	"github.com/amir1430/gamify/restrict"
	"github.com/amir1430/gamify/restrict/restrictadmin"
)

func TestSetAndLift(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	s, err := restrict.Register(ctx, a.App, restrict.Config{
		Blocks:      map[string][]string{"bets": {"game.bet"}, "swaps": {"game.swap"}},
		BlockHelp:   map[string]string{"swaps": "every swap"},
		BlockLabels: map[string]string{"swaps": "Swaps"},
	})
	if err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admintest.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := restrictadmin.Register(adm, s); err != nil {
		t.Fatal(err)
	}
	cons := admintest.Start(t, a, adm)
	op, viewer := cons.Login(admin.RoleOperator), cons.Login(admin.RoleViewer)
	alice := a.User("alice")
	id := strconv.FormatInt(alice, 10)

	// The player's tab shows their (empty) row, so a first restriction can be set from it.
	st, body := op.Do("GET", "/api/views/restrictions?user_id="+id, nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 1 || items[0].(map[string]any)["active"] != false {
		t.Fatalf("tab: %d %v", st, body)
	}
	path := "/api/views/restrictions/" + id + "/actions/"
	if st, _ := viewer.Do("POST", path+"set", map[string]any{"reason": "x", "input": map[string]any{"suspended": true}}); st != 403 {
		t.Fatalf("viewer set: %d", st)
	}
	if st, b := op.Do("POST", path+"set", map[string]any{"reason": "chargeback", "input": map[string]any{"blocks": []any{"bets"}, "note": "n"}}); st != 204 {
		t.Fatalf("set: %d %v", st, b)
	}
	if s.Check(ctx, alice, "game.bet") == nil {
		t.Fatal("block not applied at once")
	}
	st, body = op.Do("GET", "/api/views/restrictions", nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 1 || fmt.Sprint(items[0].(map[string]any)["blocks"]) != "[bets]" {
		t.Fatalf("list: %d %v", st, body)
	}
	// The row carries the form's value, so saving the form keeps what's already set.
	if st, b := op.Do("POST", path+"set", map[string]any{"reason": "again", "input": map[string]any{"blocks": []any{"nope"}}}); st != 400 {
		t.Fatalf("unknown block: %d %v", st, b)
	}
	// A restricted player's row offers lift; the page's add refuses them, so it can't overwrite.
	st, body = op.Do("GET", "/api/views/restrictions/"+id, nil)
	if st != 200 || body["_actions"] != nil {
		t.Fatalf("restricted row: %d %v", st, body)
	}
	if st, b := op.Do("POST", "/api/views/restrictions/actions/add", map[string]any{"reason": "x", "input": map[string]any{"user_id": alice, "suspended": true}}); st != 409 {
		t.Fatalf("add over a restriction: %d %v", st, b)
	}
	if s.Check(ctx, alice, "game.other") != nil {
		t.Fatal("add overwrote the restriction")
	}
	if st, _ := op.Do("POST", path+"lift", map[string]any{"reason": "appeal"}); st != 204 || s.Check(ctx, alice, "game.bet") != nil {
		t.Fatalf("lift: %d", st)
	}
	st, body = op.Do("GET", "/api/views/restrictions/"+id, nil)
	if st != 200 || !strings.Contains(fmt.Sprint(body["_actions"]), "lift:map[unavailable") {
		t.Fatalf("lifted row: %d %v", st, body)
	}
	// From the page, by player ID; the audit row names the player.
	if st, b := op.Do("POST", "/api/views/restrictions/actions/add", map[string]any{"reason": "new", "input": map[string]any{"user_id": alice, "blocks": []any{"bets"}}}); st != 204 {
		t.Fatalf("add: %d %v", st, b)
	}
	if s.Check(ctx, alice, "game.bet") == nil {
		t.Fatal("add not applied at once")
	}
	var target string
	if err := a.Pool.QueryRow(ctx, `SELECT target FROM admin_audit WHERE action = 'view.restrictions.add'`).Scan(&target); err != nil || target != "restrictions:"+id {
		t.Fatalf("audit target = %q, %v", target, err)
	}
	st, b := op.Do("POST", "/api/views/restrictions/actions/add", map[string]any{"reason": "x", "input": map[string]any{"user_id": 999999, "suspended": true}})
	if st != 400 {
		t.Fatalf("unknown player: %d %v", st, b)
	}
}
