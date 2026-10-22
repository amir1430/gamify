package matchmaking

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/matchmaking/internal/store"
)

// MatchFilter selects matches for Matches; zero fields are no filter.
type MatchFilter struct {
	UserID int64
	Queue  string
	Status string
}

// Matches lists matches newest first: before 0 is the first page, limit at most as many as given.
func (s *Service) Matches(ctx context.Context, f MatchFilter, before int64, limit int32) ([]Match, error) {
	return s.matches(ctx, f, false, before, limit)
}

func (s *Service) matches(ctx context.Context, f MatchFilter, history bool, before int64, limit int32) ([]Match, error) {
	rows, err := s.q.ListMatches(ctx, store.ListMatchesParams{BeforeID: before, Queue: f.Queue, Status: f.Status,
		History: history, UserID: f.UserID, MaxRows: limit})
	if err != nil {
		return nil, err
	}
	return s.toMatches(ctx, s.q, rows)
}

// QueueStat is a queue's waiting tickets; Oldest is the zero time when none wait.
type QueueStat struct {
	Name    string
	Waiting int64
	Oldest  time.Time
}

// QueueStats lists every registered queue with the tickets waiting in it, by name.
func (s *Service) QueueStats(ctx context.Context) ([]QueueStat, error) {
	rows, err := s.q.QueueStats(ctx)
	if err != nil {
		return nil, err
	}
	waiting := map[string]store.QueueStatsRow{}
	for _, r := range rows {
		waiting[r.Queue] = r
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QueueStat, 0, len(s.queues))
	for name := range s.queues {
		r := waiting[name]
		out = append(out, QueueStat{Name: name, Waiting: r.Waiting, Oldest: r.Oldest.Time})
	}
	slices.SortFunc(out, func(a, b QueueStat) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// RatingRow is a player's rating under a key.
type RatingRow struct {
	UserID  int64
	Key     string
	Rating  Rating
	Matches int
	ID      int64 // stable per (user, key)
}

// Ratings lists ratings of the limit highest user IDs below before (0: no bound), all keys of
// each, or of userID alone when it is not 0.
func (s *Service) Ratings(ctx context.Context, userID, before int64, limit int32) ([]RatingRow, error) {
	rows, err := s.q.ListRatings(ctx, store.ListRatingsParams{BeforeID: before, UserID: userID, MaxRows: limit})
	if err != nil {
		return nil, err
	}
	out := make([]RatingRow, len(rows))
	for i, r := range rows {
		out[i] = RatingRow{UserID: r.UserID, Key: r.Key, Rating: Rating{r.Mu, r.Sigma}, Matches: int(r.Matches), ID: r.RowID}
	}
	return out, nil
}

// history is the app.HistorySource of staked matches that ended, one activity per player: amount
// is the stake, payout what the player won, status settled or cancelled. A page is limit matches,
// so it holds more activities when it lists every user.
func (s *Service) history(ctx context.Context, userID, beforeID int64, limit int32) ([]app.Activity, error) {
	rows, err := s.q.ListMatches(ctx, store.ListMatchesParams{BeforeID: beforeID, History: true, UserID: userID, MaxRows: limit})
	if err != nil {
		return nil, err
	}
	ms, err := s.toMatches(ctx, s.q, rows)
	if err != nil {
		return nil, err
	}
	out := []app.Activity{}
	for i, m := range ms {
		var paid []payout
		if err := json.Unmarshal(rows[i].Payouts, &paid); err != nil {
			return nil, err
		}
		for _, p := range m.Players {
			if userID != 0 && p.UserID != userID {
				continue
			}
			var won int64
			for _, w := range paid {
				if w.UserID == p.UserID {
					won += w.Amount
				}
			}
			result := "lost"
			switch {
			case m.Status == "cancelled":
				result = "cancelled"
			case rows[i].Refund:
				result = "refunded"
			case won > 0:
				result = "won"
			}
			detail, _ := json.Marshal(map[string]string{"queue": m.Queue, "result": result})
			out = append(out, app.Activity{Game: "matchmaking", ID: m.ID, UserID: p.UserID, Currency: m.Currency,
				Amount: m.Stake, Payout: won, Status: m.Status, Detail: detail, At: rows[i].UpdatedAt.Time})
		}
	}
	return out, nil
}
