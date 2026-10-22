// Package matchmakingadmin adds matchmaking's pages to the admin console: matches (with cancel
// and report-result actions), queues and ratings. The brand calls Register after admin.Register
// and before App.Run.
package matchmakingadmin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking"

	"github.com/jackc/pgx/v5"
)

var statuses = []string{"forming", "running", "finishing", "settled", "cancelling", "cancelled"}

// Register adds the views mm-matches, mm-queues and mm-ratings.
func Register(adm *admin.Admin, s *matchmaking.Service) error {
	for _, v := range []admin.View{matches(s), queues(s), ratings(s)} {
		if err := adm.View(v); err != nil {
			return err
		}
	}
	return nil
}

func userFilter(q admin.Query) int64 {
	n, _ := strconv.ParseInt(q.Filters["user_id"], 10, 64) // checked by the console
	return n
}

func matchRow(m matchmaking.Match) admin.Row {
	pot := m.Stake * int64(len(m.Players))
	row := admin.Row{"id": m.ID, "queue": m.Queue, "status": m.Status, "currency": string(m.Currency), "stake": m.Stake,
		"pot": pot, "fee": ledger.FeeOf(pot, m.FeeBP), "created_at": m.CreatedAt}
	if m.Result != nil {
		ranks := make([]string, len(m.Result.Ranks))
		for i, r := range m.Result.Ranks {
			ranks[i] = strconv.Itoa(r)
		}
		row["result"] = strings.Join(ranks, ",")
	}
	return row
}

func matches(s *matchmaking.Service) admin.View {
	return admin.View{
		Name: "mm-matches", Title: "Matches", Group: "Matchmaking", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Type: admin.UserID},
			{Key: "queue", Type: admin.Text},
			{Key: "status", Type: admin.Enum, Options: statuses},
			{Key: "currency", Type: admin.Text},
			{Key: "stake", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "pot", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "fee", Type: admin.Money, CurrencyKey: "currency"},
			{Key: "result", Label: "Result (rank per team)", Type: admin.Text},
			{Key: "created_at", Type: admin.Time},
		},
		Filters: []admin.Filter{
			{Key: "user_id", Type: admin.UserID},
			{Key: "queue", Type: admin.Text},
			{Key: "status", Type: admin.Enum},
		},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			f := matchmaking.MatchFilter{UserID: userFilter(q), Queue: q.Filters["queue"], Status: q.Filters["status"]}
			ms, err := s.Matches(ctx, f, q.Before, q.Limit)
			if err != nil {
				return admin.Page{}, err
			}
			p := admin.Page{Items: []admin.Row{}}
			for _, m := range ms {
				row := matchRow(m)
				if f.UserID != 0 {
					row["user_id"] = f.UserID
				}
				p.Items = append(p.Items, row)
			}
			if len(ms) == int(q.Limit) {
				p.Next = &ms[len(ms)-1].ID
			}
			return p, nil
		},
		Get: func(ctx context.Context, id int64) (admin.Row, error) {
			m, err := s.Get(ctx, id)
			if errors.Is(err, matchmaking.ErrNotFound) {
				return nil, admin.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			return matchRow(m), nil
		},
		Actions: []admin.Action{
			{Name: "cancel", Title: "Cancel match", Confirm: "Cancel this match and refund every stake?", Perm: admin.PermMatchCancel,
				Reason: true,
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					m, err := s.Get(ctx, c.ID)
					if err != nil {
						return nil, nil, refusal(err)
					}
					if err := s.MarkCancel(ctx, tx, c.ID, c.Reason); err != nil {
						return nil, nil, refusal(err)
					}
					return map[string]any{"status": m.Status}, map[string]any{"status": "cancelling"}, nil
				}},
			{Name: "report", Title: "Report result", Perm: admin.PermMatchReport,
				Reason: true,
				Fields: []admin.Field{{Key: "ranks", Label: "Ranks, one per team, best 1 (2,1)", Type: admin.Text, Required: true}},
				Run: func(ctx context.Context, tx pgx.Tx, c admin.Call) (any, any, error) {
					ranks, err := parseRanks(c.Input["ranks"])
					if err != nil {
						return nil, nil, err
					}
					m, err := s.Get(ctx, c.ID)
					if err != nil {
						return nil, nil, refusal(err)
					}
					if m.Status == "forming" { // Report takes running matches (and repeats of a result)
						return nil, nil, admin.Conflict("the match is still forming")
					}
					teams := 0
					for _, p := range m.Players {
						teams = max(teams, p.Team+1)
					}
					if len(ranks) != teams {
						return nil, nil, admin.BadInput(fmt.Sprintf("%d ranks for %d teams", len(ranks), teams))
					}
					if err := s.Report(ctx, tx, c.ID, matchmaking.Result{Ranks: ranks}); err != nil {
						return nil, nil, refusal(err)
					}
					return map[string]any{"status": m.Status}, map[string]any{"status": "finishing", "ranks": ranks}, nil
				}},
		},
	}
}

// refusal turns what an operator can cause into the console's 400, 404 and 409.
func refusal(err error) error {
	switch {
	case errors.Is(err, matchmaking.ErrNotFound):
		return admin.ErrNotFound
	case errors.Is(err, matchmaking.ErrClosed), errors.Is(err, matchmaking.ErrResultConflict):
		return admin.Conflict(err.Error())
	}
	return err
}

// parseRanks reads "2,1" into [2 1].
func parseRanks(v any) ([]int, error) {
	text, _ := v.(string)
	var ranks []int
	for _, f := range strings.Split(text, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 {
			return nil, admin.BadInput(fmt.Sprintf("ranks %q: want positive integers like 2,1", text))
		}
		ranks = append(ranks, n)
	}
	return ranks, nil
}

func queues(s *matchmaking.Service) admin.View {
	return admin.View{
		Name: "mm-queues", Title: "Queues", Group: "Matchmaking", Perm: admin.PermView,
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "name", Type: admin.Text},
			{Key: "waiting", Type: admin.Int},
			{Key: "oldest_wait", Label: "Oldest wait (s)", Type: admin.Int},
		},
		List: func(ctx context.Context, _ admin.Query) (admin.Page, error) { // every queue in one page
			stats, err := s.QueueStats(ctx)
			if err != nil {
				return admin.Page{}, err
			}
			p := admin.Page{Items: []admin.Row{}}
			for i, st := range stats {
				var wait int64
				if st.Waiting > 0 {
					wait = int64(time.Since(st.Oldest).Seconds())
				}
				p.Items = append(p.Items, admin.Row{"id": i + 1, "name": st.Name, "waiting": st.Waiting, "oldest_wait": wait})
			}
			return p, nil
		},
	}
}

// milli is x with three decimals, as an integer.
func milli(x float64) int64 { return int64(math.Round(x * 1000)) }

func ratings(s *matchmaking.Service) admin.View {
	return admin.View{
		Name: "mm-ratings", Title: "Ratings", Group: "Matchmaking", Perm: admin.PermView, UserFilter: "user_id",
		Columns: []admin.Column{
			{Key: "id", Type: admin.Int},
			{Key: "user_id", Type: admin.UserID},
			{Key: "key", Type: admin.Text},
			{Key: "mu", Type: admin.Decimal, Scale: 3},
			{Key: "sigma", Type: admin.Decimal, Scale: 3},
			{Key: "ordinal", Type: admin.Decimal, Scale: 3},
			{Key: "matches", Type: admin.Int},
		},
		Filters: []admin.Filter{{Key: "user_id", Type: admin.UserID}},
		List: func(ctx context.Context, q admin.Query) (admin.Page, error) {
			rs, err := s.Ratings(ctx, userFilter(q), q.Before, q.Limit) // a page is Limit players
			if err != nil {
				return admin.Page{}, err
			}
			p := admin.Page{Items: []admin.Row{}}
			users := map[int64]bool{}
			var last int64
			for _, r := range rs {
				users[r.UserID], last = true, r.UserID
				p.Items = append(p.Items, admin.Row{"id": r.ID, "user_id": r.UserID, "key": r.Key, "mu": milli(r.Rating.Mu),
					"sigma": milli(r.Rating.Sigma), "ordinal": milli(r.Rating.Ordinal()), "matches": r.Matches})
			}
			if len(users) == int(q.Limit) {
				p.Next = &last
			}
			return p, nil
		},
	}
}
