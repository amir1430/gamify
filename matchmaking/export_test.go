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

// SetSettlePot replaces the ledger settlement of a finishing match, as SetHoldPot.
func SetSettlePot(f func(l *ledger.Ledger, ctx context.Context, g ledger.Game, c ledger.Currency, potID, feeBP int64,
	entries []ledger.PotEntry, winners []ledger.PotWin) (int64, error)) func() {
	old := settlePot
	settlePot = f
	return func() { settlePot = old }
}

// TickNow runs the matcher loop's tick once (a pass and the gauges).
func (s *Service) TickNow(ctx context.Context) { s.tick(ctx) }
