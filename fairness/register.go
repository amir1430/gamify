package fairness

import (
	"context"
	"embed"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/realtime"
)

// Migrations is the module's seed_pairs table (goose table goose_fairness).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Register adds provable fairness to an app: its table and the seed.info / seed.rotate RPCs. Only
// games of chance need it; they take the returned Service. The rest of the package doesn't import
// app, so the formulas don't depend on the wiring.
func Register(ctx context.Context, a *app.App, sealer *Sealer) (*Service, error) {
	if err := a.Migrate(ctx, Migrations, "goose_fairness"); err != nil {
		return nil, err
	}
	s := NewService(a.Pool, sealer)
	a.Router.Handle("seed.info", realtime.JSON(func(ctx context.Context, userID int64, _ struct{}) (Info, error) {
		return s.Info(ctx, userID)
	}))
	a.Router.Handle("seed.rotate", realtime.JSON(func(ctx context.Context, userID int64, req struct {
		ClientSeed string `json:"client_seed"`
	}) (Revealed, error) {
		if len(req.ClientSeed) > 64 {
			return Revealed{}, &realtime.ClientError{Code: realtime.CodeBadPayload, Message: "client seed too long"}
		}
		return s.Rotate(ctx, userID, req.ClientSeed)
	}))
	return s, nil
}
