package matchmaking

import (
	"context"

	"github.com/amir1430/gamify/ledger"
)

// SetHoldPot replaces the ledger hold the matcher uses; the returned func restores it. Tests that
// call it must not run in parallel.
func SetHoldPot(f func(l *ledger.Ledger, ctx context.Context, g ledger.Game, c ledger.Currency, e ledger.PotEntry) error) func() {
	old := holdPot
	holdPot = f
	return func() { holdPot = old }
}
