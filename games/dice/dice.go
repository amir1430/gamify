// Package dice: roll 0.00–99.99, stored as integer hundredths 0..9999.
package dice

import (
	"math/big"

	"github.com/amir1430/gamify/fairness"
)

const Outcomes = 10000

// Roll maps the bet hash to 0..9999 exactly like game-verify.github.io/public/dice.html.
func Roll(hash string) int { return int(fairness.SlideWindow(hash, Outcomes)) }

// WinCount is how many of the 10000 outcomes win. target is in hundredths.
// over: win when roll >= target. under: win when roll < target.
func WinCount(target int, over bool) int {
	if over {
		return Outcomes - target
	}
	return target
}

func Wins(roll, target int, over bool) bool {
	if over {
		return roll >= target
	}
	return roll < target
}

// Payout = amount * (1 - edge) / p, with p = winCount/10000, edge in basis points.
// Rounded down. Caller validates winCount > 0.
func Payout(amount int64, winCount int, edgeBP int64) int64 {
	p := new(big.Int).Mul(big.NewInt(amount), big.NewInt(10000-edgeBP))
	return p.Quo(p, big.NewInt(int64(winCount))).Int64()
}
