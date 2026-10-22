package matchmaking

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking/internal/store"
	"github.com/amir1430/gamify/metrics"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// NewMatch is a match made without tickets (a tournament round, a challenge). Teams are in team
// order; Ref names the match within the queue and must not start with "ticket:" (reserved for
// queue-formed matches).
type NewMatch struct {
	Queue    *Queue
	Teams    [][]int64
	Currency ledger.Currency
	Stake    int64
	Ref      string
}

// Create makes a match from n through the same pipeline as a queue-formed one: stakes held, OnMatch,
// running, match.found pushed. Players need not be online. The Ref makes it idempotent: a repeat
// returns the existing match in whatever state it is in (a forming one is advanced), with the same
// error a cancelled-for-funds one first returned. The other fields of a repeat are ignored. A refused
// hold cancels and refunds the match and returns ledger.ErrInsufficientFunds with the cancelled
// match; nobody is re-queued. ErrBusy: the queue is not Concurrent and a player is in one of its
// open matches. gameconfig.ErrInvalid: bad Ref, teams or stake.
func (s *Service) Create(ctx context.Context, n NewMatch) (Match, error) {
	if n.Queue == nil {
		return Match{}, ErrUnknownQueue
	}
	cfg := n.Queue.cfg
	if n.Ref == "" || strings.HasPrefix(n.Ref, "ticket:") {
		return Match{}, gameconfig.ErrInvalid
	}
	users, err := cfg.checkTeams(n.Teams)
	if err != nil {
		return Match{}, err
	}
	cur, err := cfg.checkStake(n.Currency, n.Stake)
	if err != nil {
		return Match{}, err
	}
	rs, err := s.q.UserRatings(ctx, store.UserRatingsParams{Key: cfg.RatingKey, UserIds: users})
	if err != nil {
		return Match{}, err
	}
	rating := map[int64]Rating{}
	for _, r := range rs {
		rating[r.UserID] = Rating{r.Mu, r.Sigma}
	}
	feeBP := cfg.FeeBP
	if cfg.NoFee {
		feeBP = 0
	}

	var id int64
	var status string
	var fresh bool
	err = pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if !cfg.Concurrent {
			// ponytail: per-queue lock serializes direct creates; per-user xact locks (sorted, formation locking all live users up front) if create throughput matters
			if err := q.LockQueue(ctx, cfg.Name); err != nil {
				return err
			}
		}
		status = "forming"
		id, err = q.InsertDirectMatch(ctx, store.InsertDirectMatchParams{Queue: cfg.Name, Ref: n.Ref, Currency: string(cur),
			Stake: n.Stake, FeeBp: feeBP, Deadline: pgtype.Timestamptz{Time: time.Now().Add(cfg.MatchTimeout), Valid: true}})
		if errors.Is(err, pgx.ErrNoRows) { // exists (a concurrent first call waits for its commit)
			var row store.MmMatch
			if row, err = q.MatchByRefRow(ctx, store.MatchByRefRowParams{Queue: cfg.Name, Ref: n.Ref}); err != nil {
				return err
			}
			id, status = row.ID, row.Status
			return nil
		}
		if err != nil {
			return err
		}
		fresh = true
		if !cfg.Concurrent {
			busy, err := q.UsersBusy(ctx, store.UsersBusyParams{UserIds: users, Queue: cfg.Name})
			if err != nil {
				return err
			}
			if busy {
				return ErrBusy
			}
		}
		for team, us := range n.Teams {
			for _, u := range slices.Sorted(slices.Values(us)) {
				r, ok := rating[u]
				if !ok {
					r = DefaultRating()
				}
				if err := q.InsertMatchPlayer(ctx, store.InsertMatchPlayerParams{MatchID: id, UserID: u, Team: int32(team),
					RatingMu: r.Mu, RatingSigma: r.Sigma}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Match{}, err
	}
	if fresh {
		metrics.MatchmakingMatch(cfg.Name, "formed")
	}
	if status == "forming" { // fresh, or an existing one still forming; else just report it
		if err := s.advance(ctx, id); err != nil {
			return Match{}, err
		}
	}
	m, err := s.Get(ctx, id)
	if err == nil && m.Status == "cancelled" && m.Reason == "insufficient_funds" {
		err = ledger.ErrInsufficientFunds
	}
	return m, err
}

// checkTeams validates the teams against the queue's shape and returns every user.
func (c QueueConfig) checkTeams(teams [][]int64) ([]int64, error) {
	if c.FFA != nil {
		if len(teams) < c.FFA.Min || len(teams) > c.FFA.Max {
			return nil, gameconfig.ErrInvalid
		}
	} else if len(teams) != len(c.Teams) {
		return nil, gameconfig.ErrInvalid
	}
	var users []int64
	for i, t := range teams {
		size := c.Teams[0]
		if c.FFA == nil {
			size = c.Teams[i]
		}
		if len(t) < size.Min || len(t) > size.Max {
			return nil, gameconfig.ErrInvalid
		}
		users = append(users, t...)
	}
	sorted := slices.Sorted(slices.Values(users))
	if len(slices.Compact(sorted)) != len(sorted) {
		return nil, gameconfig.ErrInvalid
	}
	return users, nil
}
