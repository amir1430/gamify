package dice_test

import (
	"context"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/games/dice"
)

// TestRPC plays Dice the way a client does, through its RPCs, on an apptest App: the pattern a
// brand copies to test its own game. Needs TEST_DATABASE_URL.
func TestRPC(t *testing.T) {
	a, ctx := apptest.New(t), context.Background()
	fair, err := fairness.Register(ctx, a.App, apptest.Sealer(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dice.Register(ctx, a.App, fair); err != nil {
		t.Fatal(err)
	}
	alice := a.User("alice")

	bet := dice.BetRequest{Amount: 100, Target: 5000, Over: true, IdemKey: "first"}
	if _, cerr := a.Call(alice, "dice.bet", bet); cerr == nil || cerr.Code != 1101 {
		t.Fatalf("bet without money = %v, want 1101 insufficient balance", cerr)
	}

	a.Fund(alice, "usd", 1_000)
	bet.IdemKey = "second"
	res, cerr := apptest.Call[dice.BetResult](a, alice, "dice.bet", bet)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if got, want := a.Balance(alice, "usd"), 1_000-res.Amount+res.Payout; got != want {
		t.Fatalf("usd = %d, want %d", got, want)
	}
	again, cerr := apptest.Call[dice.BetResult](a, alice, "dice.bet", bet)
	if cerr != nil || again.BetID != res.BetID {
		t.Fatalf("retry = %+v %v, want bet %d again", again, cerr, res.BetID)
	}
	if got, want := a.Balance(alice, "usd"), 1_000-res.Amount+res.Payout; got != want {
		t.Fatalf("usd after the retry = %d, want %d: charged twice", got, want)
	}

	// Provably fair: the revealed seed reproduces the roll.
	rev, cerr := apptest.Call[fairness.Revealed](a, alice, "seed.rotate", map[string]string{"client_seed": "mine"})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if roll := dice.Roll(fairness.Hash([]byte(rev.ServerSeed), rev.ClientSeed, res.Nonce)); roll != res.Roll {
		t.Fatalf("revealed seed gives roll %d, bet said %d", roll, res.Roll)
	}
}
