package dice

import (
	"encoding/hex"
	"testing"

	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/gameconfig"
)

// Reference rolls from game-verify.github.io/public/dice.html (see fairness_test.go).
func TestDice(t *testing.T) {
	seed, _ := hex.DecodeString("1f3a9c2e4b6d8f00112233445566778899aabbccddeeff00a1b2c3d4e5f60718")
	for nonce, want := range map[int64]int{1: 5287, 2: 1779, 3: 1170} {
		if got := Roll(fairness.Hash(fairness.Key(seed), "player-seed", nonce)); got != want {
			t.Fatalf("nonce %d: roll %d, want %d", nonce, got, want)
		}
	}
	// 50% chance, 2% edge: 100 -> 196.
	if p := Payout(100, WinCount(5000, true), HouseEdgeBP); p != 196 {
		t.Fatalf("payout = %d, want 196", p)
	}
	if !Wins(5000, 5000, true) || Wins(5000, 5000, false) {
		t.Fatal("boundary wrong")
	}
}

// A win must pay more than the stake: $0.01 over 50.50 (1.98×) would pay back its own cent.
func TestTinyBetRejected(t *testing.T) {
	for _, c := range []struct {
		amount int64
		target int
		ok     bool
	}{{1, 5050, false}, {2, 5050, true}, {1, 7550, true}, {0, 5050, true}} {
		if got := (BetRequest{Amount: c.amount, Target: c.target, Over: true, IdemKey: "k"}).valid(gameconfig.Limits{MaxPayout: MaxPayout}); got != c.ok {
			t.Fatalf("amount %d target %d: valid %v", c.amount, c.target, got)
		}
	}
}
