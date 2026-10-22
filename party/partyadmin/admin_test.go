package partyadmin_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/internal/admintest"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/party/partyadmin"
	"github.com/amir1430/gamify/realtime"
)

func TestDisband(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	p, err := party.Register(ctx, a.App, party.Config{})
	if err != nil {
		t.Fatal(err)
	}
	adm, err := admin.Register(ctx, a.App, admintest.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := partyadmin.Register(adm, p); err != nil {
		t.Fatal(err)
	}
	cons := admintest.Start(t, a, adm)
	op := cons.Login(admin.RoleOperator)
	alice, bob := a.User("alice"), a.User("bob")
	made, cerr := apptest.Call[party.Party](a, alice, "party.create", map[string]any{"max": 4, "open": true})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := apptest.Call[party.Party](a, bob, "party.join", map[string]any{"party_id": made.ID}); cerr != nil {
		t.Fatal(cerr)
	}
	var hooked []party.Party
	p.OnChange(func(_ context.Context, p party.Party) { hooked = append(hooked, p) })
	path := "/api/views/parties/" + strconv.FormatInt(made.ID, 10)

	st, body := op.Do("GET", "/api/views/parties?user_id="+strconv.FormatInt(bob, 10), nil)
	if items, _ := body["items"].([]any); st != 200 || len(items) != 1 || items[0].(map[string]any)["members"] != 2.0 {
		t.Fatalf("list: %d %v", st, body)
	}
	if st, _ := op.Do("POST", path+"/actions/disband", map[string]any{}); st != 400 { // reason required
		t.Fatalf("no reason: %d", st)
	}
	if st, _ := cons.Login(admin.RoleViewer).Do("POST", path+"/actions/disband", map[string]any{"reason": "x"}); st != 403 {
		t.Fatalf("viewer: %d", st)
	}
	if st, body := op.Do("POST", path+"/actions/disband", map[string]any{"reason": "abuse"}); st != 204 {
		t.Fatalf("disband: %d %v", st, body)
	}
	if _, err := p.Get(ctx, made.ID); err != party.ErrNotFound {
		t.Fatalf("party still there: %v", err)
	}
	for _, u := range []int64{alice, bob} {
		var left bool
		for _, raw := range a.Pushes(realtime.UserChannel(u)) {
			var m struct{ Left bool }
			if json.Unmarshal(raw, &m) == nil && m.Left {
				left = true
			}
		}
		if !left {
			t.Fatalf("user %d got no left push", u)
		}
	}
	if len(hooked) != 1 || len(hooked[0].Members) != 0 {
		t.Fatalf("hooks %+v", hooked)
	}
	var n int
	if err := a.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE action = 'view.parties.disband' AND reason = 'abuse'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows %d %v", n, err)
	}
	if st, _ := op.Do("POST", path+"/actions/disband", map[string]any{"reason": "again"}); st != 404 {
		t.Fatalf("again: %d", st)
	}
}
