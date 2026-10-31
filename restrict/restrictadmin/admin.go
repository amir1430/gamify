// Package restrictadmin adds the restrictions page to the admin console: who is suspended or
// blocked, and an action to set or lift a player's restriction (also a tab on the player page).
// The brand calls Register after admin.Register and before App.Run.
package restrictadmin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/restrict"

	"github.com/jackc/pgx/v5"
)

func row(userID int64, r restrict.Restriction, now time.Time) admin.Row {
	out := admin.Row{"id": userID, "user_id": userID, "suspended": r.Suspended, "blocks": strings.Join(r.Blocks, ", "),
		"note": r.Note, "active": r.Active(now)}
	if r.Until != nil {
		out["until"] = r.Until.Format(time.RFC3339)
	}
	if r.UpdatedAt != nil {
		out["updated_at"] = r.UpdatedAt.Format(time.RFC3339)
	}
	return out
}

// Register adds the view restrictions.
func Register(adm *admin.Admin, s *restrict.Service) error {
	fields := []admin.Field{{Key: "suspended", Label: "Suspended (no login, every action refused)", Type: admin.Bool}}
	for _, b := range s.BlockNames() {
		fields = append(fields, admin.Field{Key: "block_" + b, Label: "Block " + b, Type: admin.Bool})
	}
	fields = append(fields,
		admin.Field{Key: "until", Label: "Until (empty: until lifted)", Type: admin.Time},
		admin.Field{Key: "note", Label: "Note", Type: admin.Text})
	set := func(ctx context.Context, tx pgx.Tx, c admin.Call, r restrict.Restriction) (any, any, error) {
		before, after, err := s.Set(ctx, tx, c.ID, r)
		if err != nil {
			return nil, nil, err
		}
		return before, after, nil
	}
	applied := func(ctx context.Context, c admin.Call, _, after any) {
		s.Applied(ctx, c.ID, after.(restrict.Restriction))
	}
	return adm.View(admin.View{
		Name: "restrictions", Title: "Restrictions", Group: "Players", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Label: "Player", Type: admin.UserID},
			{Key: "active", Type: admin.Bool},
			{Key: "suspended", Type: admin.Bool},
			{Key: "blocks", Type: admin.Text},
			{Key: "until", Type: admin.Time},
			{Key: "note", Type: admin.Text},
			{Key: "updated_at", Type: admin.Time},
		},
		Filters: []admin.Filter{{Key: "user_id", Label: "Player", Type: admin.UserID}},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			page, now := admin.Page{Items: []admin.Row{}}, time.Now()
			// One player (their tab): their row, unrestricted if none, so it can be set.
			if v := q.Filters["user_id"]; v != "" {
				user, _ := strconv.ParseInt(v, 10, 64) // checked by the console
				if q.Before != 0 {
					return page, nil
				}
				r, err := s.Get(ctx, user)
				if err != nil {
					return page, err
				}
				page.Items = append(page.Items, row(user, r, now))
				return page, nil
			}
			rows, err := s.List(ctx, q.Before, q.Limit)
			if err != nil {
				return page, err
			}
			for _, x := range rows {
				page.Items = append(page.Items, row(x.UserID, x.Restriction, now))
			}
			if len(rows) == int(q.Limit) {
				page.Next = &rows[len(rows)-1].UserID
			}
			return page, nil
		},
		Get: func(ctx context.Context, id int64) (admin.Row, error) {
			r, err := s.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			return row(id, r, time.Now()), nil
		},
		Actions: []admin.Action{
			{
				Name: "set", Title: "Set restriction", Perm: admin.PermRestrict, Reason: true, Fields: fields,
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					r := restrict.Restriction{Blocks: []string{}}
					r.Suspended, _ = c.Input["suspended"].(bool)
					for _, b := range s.BlockNames() {
						if on, _ := c.Input["block_"+b].(bool); on {
							r.Blocks = append(r.Blocks, b)
						}
					}
					if t, ok := c.Input["until"].(time.Time); ok {
						if !t.After(time.Now()) {
							return nil, nil, admin.BadInput("until must be in the future")
						}
						r.Until = &t
					}
					r.Note, _ = c.Input["note"].(string)
					return set(ctx, tx, c, r)
				},
				After: applied,
			},
			{
				Name: "lift", Title: "Lift every restriction", Confirm: "Lift this player's restrictions?",
				Perm: admin.PermRestrict, Reason: true,
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					return set(ctx, tx, c, restrict.Restriction{})
				},
				After: applied,
			},
		},
	})
}
