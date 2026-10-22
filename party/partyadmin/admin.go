// Package partyadmin adds the parties page to the admin console, with a disband action. The brand
// calls Register after admin.Register and before App.Run.
package partyadmin

import (
	"context"
	"errors"
	"strconv"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/party"

	"github.com/jackc/pgx/v5"
)

func row(p party.Party) admin.Row {
	return admin.Row{"id": p.ID, "leader_id": p.LeaderID, "name": p.Name, "max": p.Max, "open": p.Open,
		"locked": p.Locked, "members": len(p.Members)}
}

// Register adds the view parties.
func Register(adm *admin.Admin, p *party.Service) error {
	return adm.View(admin.View{
		Name: "parties", Title: "Parties", Group: "Parties", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Label: "Member", Type: admin.UserID},
			{Key: "leader_id", Type: admin.UserID},
			{Key: "name", Type: admin.Text},
			{Key: "max", Type: admin.Int},
			{Key: "open", Type: admin.Bool},
			{Key: "locked", Type: admin.Text},
			{Key: "members", Type: admin.Int},
		},
		Filters: []admin.Filter{{Key: "user_id", Label: "Member", Type: admin.UserID}},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			user, _ := strconv.ParseInt(q.Filters["user_id"], 10, 64) // checked by the console
			ps, err := p.List(ctx, user, q.Before, q.Limit)
			if err != nil {
				return admin.Page{}, err
			}
			page := admin.Page{Items: []admin.Row{}}
			for _, x := range ps {
				r := row(x)
				if user != 0 {
					r["user_id"] = user
				}
				page.Items = append(page.Items, r)
			}
			if len(ps) == int(q.Limit) {
				page.Next = &ps[len(ps)-1].ID
			}
			return page, nil
		},
		Get: func(ctx context.Context, id int64) (admin.Row, error) {
			x, err := p.Get(ctx, id)
			if errors.Is(err, party.ErrNotFound) {
				return nil, admin.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			return row(x), nil
		},
		Actions: []admin.Action{{
			Name: "disband", Title: "Disband party", Confirm: "Remove every member and delete the party?",
			Perm: admin.PermPartyDisband, Reason: true,
			Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
				x, err := p.Disband(ctx, tx, c.ID)
				if errors.Is(err, party.ErrNotFound) {
					return nil, nil, admin.ErrNotFound
				}
				if err != nil {
					return nil, nil, err
				}
				return x, nil, nil
			},
			// the members' pushes and OnChange hooks, once the party is really gone
			After: func(ctx context.Context, _ admin.Call, before, _ any) { p.Disbanded(ctx, before.(party.Party)) },
		}},
	})
}
