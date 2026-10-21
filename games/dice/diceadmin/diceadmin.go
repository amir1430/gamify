// Package diceadmin adds Dice's view to the admin console: the worked example of a game's admin
// view. It lives apart from dice so the game doesn't import admin; a brand that runs the console
// calls Register after admin.Register.
package diceadmin

import (
	"context"
	"strconv"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/games/dice"
)

var statuses = []string{"pending", "settled", "voided"}

// Register adds the "dice-bets" view: every dice bet, filtered by player and status, and a tab
// on the player page. Read-only (admin.PermView), no actions.
func Register(adm *admin.Admin, s *dice.Service) error {
	return adm.View(admin.View{
		Name: "dice-bets", Title: "Dice bets", Group: "Dice", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Label: "ID", Type: admin.Int},
			{Key: "user_id", Label: "Player", Type: admin.UserID},
			{Key: "amount", Label: "Amount", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "payout", Label: "Payout", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "target", Label: "Target", Type: admin.Decimal, Scale: 2},
			{Key: "over", Label: "Over", Type: admin.Bool},
			{Key: "roll", Label: "Roll", Type: admin.Decimal, Scale: 2},
			{Key: "status", Label: "Status", Type: admin.Enum, Options: statuses},
			{Key: "created_at", Label: "Placed", Type: admin.Time},
		},
		Filters: []admin.Filter{
			{Key: "user_id", Label: "Player", Type: admin.UserID},
			{Key: "status", Label: "Status", Type: admin.Enum, Options: statuses},
		},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) { return list(ctx, s, q) },
	})
}

func list(ctx context.Context, s *dice.Service, q admin.Query) (admin.Page, error) {
	var user *int64
	if v, ok := q.Filters["user_id"]; ok {
		n, err := strconv.ParseInt(v, 10, 64) // checked by the console already
		if err != nil {
			return admin.Page{}, err
		}
		user = &n
	}
	bets, err := s.ListBets(ctx, user, q.Filters["status"], q.Before, q.Limit)
	if err != nil {
		return admin.Page{}, err
	}
	p := admin.Page{Items: make([]admin.Row, len(bets))}
	for i, b := range bets {
		p.Items[i] = admin.Row{"id": b.ID, "user_id": b.UserID, "amount": b.Amount, "currency": b.Currency,
			"payout": b.Payout, "target": b.Target, "over": b.Over, "roll": b.Roll, "status": b.Status,
			"created_at": b.CreatedAt}
	}
	if len(bets) == int(q.Limit) && len(bets) > 0 {
		n := bets[len(bets)-1].ID
		p.Next = &n
	}
	return p, nil
}
