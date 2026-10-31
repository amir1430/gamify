package restrict

import (
	"context"
	"embed"

	"github.com/amir1430/gamify/app"
)

// Migrations is the module's table (goose table goose_restrict).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Register adds restrictions to an app: its table, the login check, the RPC check (Router.CheckData,
// after any the brand set before) and the reload loop. restrictadmin adds the console page.
func Register(ctx context.Context, a *app.App, cfg Config) (*Service, error) {
	s, err := New(a.Pool, cfg)
	if err != nil {
		return nil, err
	}
	if err := a.Migrate(ctx, Migrations, "goose_restrict"); err != nil {
		return nil, err
	}
	if err := s.Load(ctx); err != nil {
		return nil, err
	}
	s.kick = a.Realtime.Disconnect
	prev := a.Router.CheckData
	a.Router.CheckData = func(ctx context.Context, userID int64, method string, data []byte) error {
		if prev != nil {
			if err := prev(ctx, userID, method, data); err != nil {
				return err
			}
		}
		return s.CheckCall(ctx, userID, method, data)
	}
	a.LoginCheck(s.Login)
	a.Loop(s.cfg.ReloadEvery, s.reload)
	return s, nil
}
