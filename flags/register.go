package flags

import (
	"context"
	"embed"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/realtime"
)

// Migrations is the module's tables (goose table goose_flags).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// GetResponse is what the RPC flags.get returns: the caller's value for every flag, by key.
type GetResponse struct {
	Flags map[string]any `json:"flags"`
}

// Register adds flags to an app: its tables, the RPC flags.get and the reload loop. flagsadmin
// adds the console page.
func Register(ctx context.Context, a *app.App, cfg Config) (*Service, error) {
	s, err := New(a.Pool, cfg)
	if err != nil {
		return nil, err
	}
	if err := a.Migrate(ctx, Migrations, "goose_flags"); err != nil {
		return nil, err
	}
	if err := s.Load(ctx); err != nil {
		return nil, err
	}
	a.Router.Handle("flags.get", realtime.JSON(func(_ context.Context, userID int64, _ struct{}) (GetResponse, error) {
		return GetResponse{Flags: s.For(userID)}, nil
	}))
	a.Loop(s.cfg.ReloadEvery, s.reload)
	return s, nil
}
