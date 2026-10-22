package admin

import (
	"context"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/apptest"
)

// arenaMax is the test game's code cap on one amount.
const arenaMax = 1_000_000

// registerArena adds a test game, "arena": default limits and a fixed history (rows newest first),
// no RPCs.
func registerArena(a *apptest.App, rows ...app.Activity) {
	a.Games.Game("arena", arenaMax, 98_000_000, nil)
	a.History("arena", func(_ context.Context, userID, beforeID int64, limit int32) ([]app.Activity, error) {
		var out []app.Activity
		for _, b := range rows {
			if (userID == 0 || b.UserID == userID) && (beforeID == 0 || b.ID < beforeID) && len(out) < int(limit) {
				out = append(out, b)
			}
		}
		return out, nil
	})
}

func arenaRow(id, userID int64) app.Activity {
	return app.Activity{Game: "arena", ID: id, UserID: userID, Currency: "usd", Amount: 100, Status: "settled", At: time.Now()}
}
