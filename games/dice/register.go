package dice

import (
	"context"
	"embed"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/fairness"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/realtime"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Register adds Dice to an app: its tables, the dice.bet RPC and its reconciler. Dice is a game of
// chance: it draws from fair, which fairness.Register returns.
func Register(ctx context.Context, a *app.App, fair *fairness.Service) (*Service, error) {
	if err := a.Ledger.RegisterGame(Game, "dice"); err != nil {
		return nil, err
	}
	if err := a.Migrate(ctx, migrations, "goose_dice"); err != nil {
		return nil, err
	}
	s := NewService(a.Pool, fair, a.Ledger, a.Games)
	a.Router.Handle("dice.bet", realtime.JSON(func(ctx context.Context, userID int64, req BetRequest) (any, error) {
		res, err := s.Bet(ctx, userID, req)
		if err != nil {
			return nil, err
		}
		b, err := a.Balances(ctx, userID)
		if err != nil {
			return nil, err
		}
		return struct {
			BetResult
			Balances ledger.Balances `json:"balances"`
		}{res, b}, nil
	}))
	a.Bets("dice", s.History)
	a.Reconcile(s.Reconcile)
	return s, nil
}
