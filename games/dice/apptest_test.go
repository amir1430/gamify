package dice_test

import (
	"context"
	"strings"
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

	// Bet history, newest first, for the admin.
	bet.IdemKey = "third"
	res2, cerr := apptest.Call[dice.BetResult](a, alice, "dice.bet", bet)
	if cerr != nil {
		t.Fatal(cerr)
	}
	hist, err := a.BetSources()["dice"](ctx, alice, 0, 10)
	if err != nil || len(hist) != 3 {
		t.Fatalf("history = %+v, %v; want 3 bets (the unfunded try is a voided bet)", hist, err)
	}
	if hist[0].ID != res2.BetID || hist[1].ID != res.BetID || hist[0].Game != "dice" ||
		hist[0].Amount != res2.Amount || hist[1].Amount != res.Amount || hist[0].UserID != alice || hist[2].Status != "voided" ||
		!strings.Contains(string(hist[0].Detail), `"roll"`) {
		t.Fatalf("history = %+v", hist)
	}

	if page, err := a.BetSources()["dice"](ctx, alice, hist[1].ID, 10); err != nil || len(page) != 1 || page[0].ID != hist[2].ID {
		t.Fatalf("page after bet %d = %+v, %v", hist[1].ID, page, err)
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
