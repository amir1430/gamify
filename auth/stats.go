package auth

import (
	"context"
	"fmt"
	"time"
)

// Stats counts players: all of them, those who signed up and those who logged in within the last
// day, week (7 days) and month (30 days).
type Stats struct {
	Total     int64 `json:"total"`
	New24h    int64 `json:"new_24h"`
	New7d     int64 `json:"new_7d"`
	New30d    int64 `json:"new_30d"`
	Active24h int64 `json:"active_24h"`
	Active7d  int64 `json:"active_7d"`
	Active30d int64 `json:"active_30d"`
}

// Stats counts the players (see Stats).
func (u *Users) Stats(ctx context.Context) (Stats, error) {
	r, err := u.q.UserStats(ctx)
	return Stats{r.Total, r.New1d, r.New7d, r.New30d, r.Active1d, r.Active7d, r.Active30d}, err
}

// DayCount is a count for one UTC day (Day is its midnight, UTC).
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

// MaxSignupDays is the longest window Signups reads.
const MaxSignupDays = 366

// Signups counts sign-ups per UTC day for the last days days (1..MaxSignupDays), oldest first,
// today last, days without any as 0.
func (u *Users) Signups(ctx context.Context, days int) ([]DayCount, error) {
	if days < 1 || days > MaxSignupDays {
		return nil, fmt.Errorf("auth: Signups days %d, want 1..%d", days, MaxSignupDays)
	}
	rows, err := u.q.SignupsByDay(ctx, int32(days))
	if err != nil {
		return nil, err
	}
	out := make([]DayCount, len(rows))
	for i, r := range rows {
		out[i] = DayCount{time.Date(r.Day.Time.Year(), r.Day.Time.Month(), r.Day.Time.Day(), 0, 0, 0, 0, time.UTC), r.Signups}
	}
	return out, nil
}
