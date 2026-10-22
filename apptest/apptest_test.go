package apptest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/realtime"
)

func TestCallAndFund(t *testing.T) {
	a := apptest.New(t)
	alice := a.User("alice")
	if again := a.User("alice"); again != alice {
		t.Fatalf("User(alice) twice = %d, %d", alice, again)
	}
	if bob := a.User("bob"); bob == alice {
		t.Fatal("bob is alice")
	}

	b, cerr := apptest.Call[ledger.Balances](a, alice, "wallet.balances", nil)
	if cerr != nil || b["usd"] != 0 || b["tickets"] != 1000 {
		t.Fatalf("balances = %v %v: want the welcome grant", b, cerr)
	}
	a.Fund(alice, "usd", 5_000)
	if got := a.Balance(alice, "usd"); got != 5_000 {
		t.Fatalf("usd = %d after funding", got)
	}

	if _, cerr := a.Call(alice, "nope.nope", nil); cerr == nil || cerr.Code != realtime.CodeUnknownMethod {
		t.Fatalf("unknown method = %v", cerr)
	}
}

func TestPushes(t *testing.T) {
	a := apptest.New(t)
	alice := a.User("alice")
	if _, err := a.Balances(context.Background(), alice); err != nil { // publishes to alice's channel
		t.Fatal(err)
	}
	pushes := a.Pushes(realtime.UserChannel(alice))
	if len(pushes) != 1 {
		t.Fatalf("pushes = %d, want 1", len(pushes))
	}
	var msg struct{ Type string }
	if err := json.Unmarshal(pushes[0], &msg); err != nil || msg.Type != "balances" {
		t.Fatalf("push = %s", pushes[0])
	}
}

func TestReconcile(t *testing.T) {
	a := apptest.New(t)
	runs := 0
	a.Reconcile(func(context.Context) { runs++ })
	a.ReconcileNow()
	a.ReconcileNow()
	if runs != 2 {
		t.Fatalf("reconcile ran %d times, want 2", runs)
	}
}

// Each App has its own database: users and tables of one test never show up in another.
func TestIsolated(t *testing.T) {
	one, two := apptest.New(t), apptest.New(t)
	if one.User("alice") != two.User("alice") {
		t.Fatal("two fresh databases gave alice different first IDs")
	}
	one.Fund(one.User("alice"), "usd", 100)
	if got := two.Balance(two.User("alice"), "usd"); got != 0 {
		t.Fatalf("second app sees the first one's money: %d", got)
	}
}

func TestSetOnline(t *testing.T) {
	a := apptest.New(t)
	if on, _ := a.Transport.Online(context.Background()); len(on) != 0 {
		t.Fatalf("default online = %v, want empty", on)
	}
	a.Transport.SetOnline(1, 2)
	on, err := a.Transport.Online(context.Background())
	if err != nil || len(on) != 2 || on[1] != 1 || on[2] != 1 {
		t.Fatalf("online = %v, %v", on, err)
	}
}
