package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/amir1430/gamify/ledger"
)

// Bet is one bet of any game, as the admin lists it.
type Bet struct {
	Game     string          `json:"game"`
	ID       int64           `json:"id"`
	UserID   int64           `json:"user_id"`
	Currency ledger.Currency `json:"currency"`
	Amount   int64           `json:"amount"`
	Payout   int64           `json:"payout"`
	Status   string          `json:"status"`
	Detail   json.RawMessage `json:"detail,omitempty"` // the game's own fields
	At       time.Time       `json:"at"`
}

// BetSource lists a game's bets newest first: userID 0 means every user, beforeID 0 the first page.
type BetSource func(ctx context.Context, userID, beforeID int64, limit int32) ([]Bet, error)

// Bets registers a game's bet history. It panics if the game is already registered.
func (a *App) Bets(game string, f BetSource) {
	a.betsMu.Lock()
	defer a.betsMu.Unlock()
	if _, ok := a.bets[game]; ok {
		panic(fmt.Sprintf("app: bet history of %q registered twice", game))
	}
	if a.bets == nil {
		a.bets = map[string]BetSource{}
	}
	a.bets[game] = f
}

// BetSources returns a copy of the registered bet histories by game name.
func (a *App) BetSources() map[string]BetSource {
	a.betsMu.Lock()
	defer a.betsMu.Unlock()
	return maps.Clone(a.bets)
}
