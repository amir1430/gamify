package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/amir1430/gamify/ledger"
)

// Activity is one operation of any game (a round, a match, a purchase), as the admin lists it. A
// game without money leaves Amount and Payout 0 and puts its own fields (score, level) in Detail.
type Activity struct {
	Game     string          `json:"game"`
	ID       int64           `json:"id"`
	UserID   int64           `json:"user_id"`
	Currency ledger.Currency `json:"currency"`
	Amount   int64           `json:"amount"`
	Payout   int64           `json:"payout"`
	Status   string          `json:"status"`
	Detail   json.RawMessage `json:"detail,omitempty"` // the game's own fields; never an unrevealed server seed
	At       time.Time       `json:"at"`
}

// HistorySource lists a game's activity newest first: userID 0 means every user, beforeID 0 the
// first page.
type HistorySource func(ctx context.Context, userID, beforeID int64, limit int32) ([]Activity, error)

// History registers a game's activity history. It panics if the game is already registered.
func (a *App) History(game string, f HistorySource) {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if _, ok := a.history[game]; ok {
		panic(fmt.Sprintf("app: history of %q registered twice", game))
	}
	if a.history == nil {
		a.history = map[string]HistorySource{}
	}
	a.history[game] = f
}

// HistorySources returns a copy of the registered activity histories by game name.
func (a *App) HistorySources() map[string]HistorySource {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	return maps.Clone(a.history)
}
