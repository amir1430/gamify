package restrictadmin_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/internal/admintest"
	"github.com/amir1430/gamify/restrict"
	"github.com/amir1430/gamify/restrict/restrictadmin"
)

func TestSetAndLift(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	s, err := restrict.Register(ctx, a.App, restrict.Config{Blocks: map[string][]string{"bets": {"game.bet"}}})
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
	if st, b := op.Do("POST", path+"set", map[string]any{"reason": "chargeback", "input": map[string]any{"block_bets": true, "note": "n"}}); st != 204 {
		t.Fatalf("set: %d %v", st, b)
	}
	if s.Check(ctx, alice, "game.bet") == nil {
		t.Fatal("block not applied at once")
	}
	st, body = op.Do("GET", "/api/views/restrictions", nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 1 || items[0].(map[string]any)["blocks"] != "bets" {
		t.Fatalf("list: %d %v", st, body)
	}
	if st, _ := op.Do("POST", path+"lift", map[string]any{"reason": "appeal"}); st != 204 || s.Check(ctx, alice, "game.bet") != nil {
		t.Fatalf("lift: %d", st)
	}
}
