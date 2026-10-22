package matchmaking

import (
	"errors"
	"testing"

	"github.com/amir1430/gamify/ledger"
)

func TestPayoutsTooManyWinners(t *testing.T) {
	m := Match{ID: 1, Stake: 10, FeeBP: 0, Result: &Result{Ranks: make([]int, ledger.MaxPotWinners+1)}}
	for i := range m.Result.Ranks {
		m.Result.Ranks[i] = 1
		m.Players = append(m.Players, Player{EntryID: int64(i + 1), UserID: int64(i + 1), Team: i})
	}
	if _, _, err := payouts(QueueConfig{}, m); !errors.Is(err, ledger.ErrTooManyWinners) {
		t.Fatalf("default: %v", err)
	}
	m.Result.Ranks = m.Result.Ranks[:ledger.MaxPotWinners] // one team fewer: exactly the limit
	m.Players = m.Players[:ledger.MaxPotWinners]
	if w, _, err := payouts(QueueConfig{}, m); err != nil || len(w) != ledger.MaxPotWinners {
		t.Fatalf("at the limit: %d %v", len(w), err)
	}
	all := QueueConfig{Payout: func(pot int64, m Match, r Result) (ws []ledger.PotWin) {
		for range ledger.MaxPotWinners + 1 {
			ws = append(ws, ledger.PotWin{UserID: 1, Amount: 1})
		}
		ws[0].Amount = pot - int64(ledger.MaxPotWinners)
		return
	}}
	if _, _, err := payouts(all, m); !errors.Is(err, ledger.ErrTooManyWinners) {
		t.Fatalf("hook: %v", err)
	}
}
