package matchmaking_test

import (
	"context"
	"testing"

	"github.com/amir1430/gamify/apptest"
	"github.com/amir1430/gamify/matchmaking"
	"github.com/amir1430/gamify/party"
)

func TestRegister(t *testing.T) {
	a := apptest.New(t)
	ctx := context.Background()
	p, err := party.Register(ctx, a.App, party.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matchmaking.Register(ctx, a.App, p, matchmaking.Config{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.Pool.QueryRow(ctx, `SELECT count(*) FROM mm_tickets, mm_ticket_users, mm_requests, mm_matches, mm_match_players, mm_ratings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
}
