package matchmaking

import (
	"maps"
	"math"
	"slices"
	"time"
)

// formed is one match the matcher found: its tickets and the user IDs per team.
type formed struct {
	Tickets []int64
	Teams   [][]int64
}

// form groups a queue's tickets (sorted by QueuedAt, ties by ID) into matches. The oldest unused
// ticket anchors a group; an anchor that cannot form stays queued and the next one is tried.
// ponytail: O(n²) per queue per tick; bucket by properties if a queue outgrows it.
func form(q *Queue, tickets []Ticket, now time.Time) []formed {
	used := make([]bool, len(tickets))
	var out []formed
	for a := range tickets {
		if used[a] {
			continue
		}
		if f, group := build(q.cfg, tickets, used, a, now); group != nil {
			for _, i := range group {
				used[i] = true
			}
			out = append(out, f)
		}
	}
	return out
}

// build fills teams around anchor a with whole tickets: the anchor first, then larger parties
// first (oldest first among equals) so small parties don't strand a big one. Each unit goes to the
// team with the most room it fits in, or in a ranked queue the fitting team with the lowest rating.
func build(c QueueConfig, ts []Ticket, used []bool, a int, now time.Time) (formed, []int) {
	anchor, wait := ts[a], now.Sub(ts[a].QueuedAt)
	cand := []int{a}
	for i := a + 1; i < len(ts); i++ {
		if !used[i] && fits(c, anchor, ts[i], wait) {
			cand = append(cand, i)
		}
	}
	slices.SortStableFunc(cand[1:], func(x, y int) int { return len(ts[y].Users) - len(ts[x].Users) })

	teams := c.Teams
	if c.FFA != nil {
		teams = slices.Repeat([]Team{{1, 1}}, c.FFA.Max)
	}
	members := make([][]int, len(teams))
	size, sum := make([]int, len(teams)), make([]float64, len(teams))
	full := func() bool {
		for t := range teams {
			if size[t] < teams[t].Max {
				return false
			}
		}
		return true
	}
	var group []int
	for _, i := range cand {
		if full() {
			break
		}
		if slices.ContainsFunc(group, func(j int) bool { return c.Compatible != nil && !c.Compatible(ts[j], ts[i]) }) {
			continue
		}
		n, best := len(ts[i].Users), -1
		for t := range teams {
			if size[t]+n > teams[t].Max {
				continue
			}
			if best < 0 || c.Ranked && sum[t] < sum[best] ||
				!c.Ranked && teams[t].Max-size[t] > teams[best].Max-size[best] {
				best = t
			}
		}
		if best < 0 {
			continue
		}
		members[best] = append(members[best], i)
		size[best] += n
		sum[best] += ts[i].Rating * float64(n)
		group = append(group, i)
	}

	ready := full()
	if !ready && wait >= c.FillAfter {
		if c.FFA != nil {
			ready = len(group) >= c.FFA.Min
		} else {
			ready = true
			for t := range teams {
				ready = ready && size[t] >= teams[t].Min
			}
		}
	}
	if !ready {
		return formed{}, nil
	}
	var f formed
	for _, i := range group {
		f.Tickets = append(f.Tickets, ts[i].ID)
	}
	for _, m := range members {
		if len(m) == 0 { // an unfilled FFA seat
			continue
		}
		var users []int64
		for _, i := range m {
			users = append(users, ts[i].Users...)
		}
		f.Teams = append(f.Teams, users)
	}
	return f, group
}

// fits reports whether t may join anchor's group, given how long the anchor has waited.
func fits(c QueueConfig, anchor, t Ticket, wait time.Duration) bool {
	if t.Currency != anchor.Currency || t.Stake != anchor.Stake ||
		!maps.Equal(t.Properties, anchor.Properties) || !maps.Equal(t.Server, anchor.Server) {
		return false
	}
	for k, r := range c.Numbers {
		d := t.Numbers[k] - anchor.Numbers[k]
		if max(d, -d) > r.Diff+r.Widen*int64(wait/r.Every) {
			return false
		}
	}
	return !c.Ranked ||
		math.Abs(t.Rating-anchor.Rating) <= c.RatingWindow+c.RatingWiden*float64(wait/(10*time.Second))
}
