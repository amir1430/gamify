// Package flagsadmin adds the feature flags page to the admin console: create a flag, turn it on
// or off, set its rollout percent and testers, delete it. The brand calls Register after
// admin.Register and before App.Run.
package flagsadmin

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/flags"

	"github.com/jackc/pgx/v5"
)

func row(f flags.Flag) admin.Row {
	ids := make([]string, len(f.Testers))
	for i, u := range f.Testers {
		ids[i] = strconv.FormatInt(u, 10)
	}
	return admin.Row{"id": f.ID, "key": f.Key, "description": f.Description, "enabled": f.Enabled,
		"percent": f.Percent, "testers": len(f.Testers), "tester_ids": ids,
		"updated_at": f.UpdatedAt.Format(time.RFC3339)}
}

// consoleErr turns the service's errors into the console's.
func consoleErr(err error) error {
	switch {
	case errors.Is(err, flags.ErrBadKey), errors.Is(err, flags.ErrExists), errors.Is(err, flags.ErrBadPercent),
		errors.Is(err, flags.ErrBadType), errors.Is(err, flags.ErrUnknownPlayer):
		return admin.BadInput(err.Error())
	case errors.Is(err, flags.ErrNotFound):
		return admin.Conflict("the flag was deleted")
	}
	return err
}

func percent(c admin.Call) int {
	n, _ := c.Input["percent"].(int64)
	return int(n)
}

// Register adds the view flags.
func Register(adm *admin.Admin, s *flags.Service) error {
	settings := []admin.Field{
		{Key: "description", Type: admin.Text, Help: "What the flag changes, for operators."},
		{Key: "enabled", Label: "On", Type: admin.Bool, Help: "Off: nobody gets it, testers included."},
		{Key: "percent", Label: "Rollout %", Type: admin.Int,
			Help: "0 to 100: the share of players who get it while on. Raising it only adds players."},
	}
	applied := func(ctx context.Context, _ admin.Call, _, _ any) { s.Applied(ctx) }
	return adm.View(admin.View{
		Name: "flags", Title: "Feature flags", Group: "Games", Perm: admin.PermView,
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "key", Type: admin.Text},
			{Key: "description", Type: admin.Text},
			{Key: "enabled", Label: "On", Type: admin.Bool},
			{Key: "percent", Label: "Rollout %", Type: admin.Int},
			{Key: "testers", Type: admin.Int},
			{Key: "updated_at", Type: admin.Time},
		},
		Summary: []string{"key", "enabled", "percent"},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			page := admin.Page{Items: []admin.Row{}}
			if q.Before != 0 { // one page holds every flag
				return page, nil
			}
			all, err := s.List(ctx)
			if err != nil {
				return page, err
			}
			for _, f := range all {
				page.Items = append(page.Items, row(f))
			}
			return page, nil
		},
		Get: func(ctx context.Context, id int64) (admin.Row, error) {
			f, err := s.Get(ctx, id)
			if errors.Is(err, flags.ErrNotFound) {
				return nil, admin.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			return row(f), nil
		},
		Actions: []admin.Action{
			{
				Name: "edit", Title: "Edit", Perm: admin.PermFlags, Fields: settings,
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					d, _ := c.Input["description"].(string)
					on, _ := c.Input["enabled"].(bool)
					before, after, err := s.Update(ctx, tx, flags.Flag{ID: c.ID, Description: d, Enabled: on, Percent: percent(c)})
					if err != nil {
						return nil, nil, consoleErr(err)
					}
					return before, after, nil
				},
				After: applied,
			},
			{
				Name: "testers", Title: "Testers", Perm: admin.PermFlags,
				Fields: []admin.Field{{Key: "tester_ids", Label: "Testers", Type: admin.Strings,
					Help: "Player IDs who see this flag whenever it is on, even at 0%."}},
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					in, _ := c.Input["tester_ids"].([]string)
					ids := make([]int64, 0, len(in))
					for _, v := range in {
						n, err := strconv.ParseInt(v, 10, 64)
						if err != nil || n <= 0 {
							return nil, nil, admin.BadInput("not a player ID: " + v)
						}
						ids = append(ids, n)
					}
					before, after, err := s.SetTesters(ctx, tx, c.ID, ids)
					if err != nil {
						return nil, nil, consoleErr(err)
					}
					return before, after, nil
				},
				After: applied,
			},
			{
				Name: "delete", Title: "Delete", Perm: admin.PermFlags,
				Confirm:     "Delete this flag? Games fall back to their default value for it.",
				TypeConfirm: "delete",
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					before, err := s.Delete(ctx, tx, c.ID)
					if err != nil {
						return nil, nil, consoleErr(err)
					}
					return before, nil, nil
				},
				After: applied,
			},
		},
		PageActions: []admin.Action{{
			Name: "new", Title: "New flag", Perm: admin.PermFlags,
			Fields: append([]admin.Field{{Key: "key", Type: admin.Text, Required: true,
				Help: "What game code reads, e.g. new_lobby: lowercase letters, digits, _ . -; can't be changed later."}}, settings...),
			Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
				k, _ := c.Input["key"].(string)
				d, _ := c.Input["description"].(string)
				on, _ := c.Input["enabled"].(bool)
				f, err := s.Create(ctx, tx, flags.Flag{Key: k, Description: d, Enabled: on, Percent: percent(c)})
				if err != nil {
					return nil, nil, consoleErr(err)
				}
				return nil, f, nil
			},
			After: applied,
		}},
	})
}
