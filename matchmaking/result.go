package matchmaking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking/internal/store"

	"github.com/jackc/pgx/v5"
)

// Finish records r as the result of running match id, then settles it: the pot paid out (fee to
// the house), ratings updated (Ranked), OnFinish called and {"type":"match.finished", match,
// payouts, ratings} pushed to every player. The same result again is a no-op (and finishes a
// settlement left undone); a different one is ErrResultConflict; a cancelled match is ErrClosed.
// The result is stored first: when the settlement fails (a ledger outage, a wrong Payout, an
// OnFinish error) Finish returns the error, the match stays finishing and the reconciler retries.
func (s *Service) Finish(ctx context.Context, id int64, r Result) error {
	if err := pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error { return s.Report(ctx, tx, id, r) }); err != nil {
		return err
	}
	return s.advance(ctx, id)
}

// Report is Finish without the settlement, in the caller's transaction (an admin's "report
// result"): it checks r as Finish does, stores it and marks the match finishing. The reconciler
// moves the money.
func (s *Service) Report(ctx context.Context, tx pgx.Tx, id int64, r Result) error {
	q := s.q.WithTx(tx)
	m, err := s.lockWait(ctx, q, id)
	if err != nil {
		return err
	}
	if n := teams(m); len(r.Ranks) == 0 || len(r.Ranks) != n || slices.Min(r.Ranks) < 1 {
		return fmt.Errorf("matchmaking: match %d: ranks %v for %d teams", id, r.Ranks, n)
	}
	switch m.Status {
	case "finishing", "settled":
		if slices.Equal(m.Result.Ranks, r.Ranks) {
			return nil
		}
		return ErrResultConflict
	case "cancelling", "cancelled":
		return ErrClosed
	case "running":
	default:
		return fmt.Errorf("matchmaking: match %d is %s", id, m.Status)
	}
	queue, err := s.queueOf(m.Queue)
	if err != nil {
		return err
	}
	m.Result = &r
	if queue.cfg.Payout == nil { // the default payout can be refused now; a hook's is checked when settling
		if _, _, err := payouts(queue.cfg, m); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = q.SetMatchFinishing(ctx, store.SetMatchFinishingParams{ID: id, Result: raw})
	return err
}

// Cancel cancels match id and refunds every stake; each player gets {"type":"match.cancelled",
// match, reason}. A forming or running match can be cancelled, a cancelled one again is a no-op, a
// finishing or settled one is ErrClosed (its result stands). When the refund fails Cancel returns
// the error, the match stays cancelling and the reconciler retries.
func (s *Service) Cancel(ctx context.Context, id int64, reason string) error {
	if err := pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error { return s.MarkCancel(ctx, tx, id, reason) }); err != nil {
		return err
	}
	return s.advance(ctx, id)
}

// MarkCancel is Cancel without the refund, in the caller's transaction (an admin action): it marks
// the match cancelling. The reconciler moves the money.
func (s *Service) MarkCancel(ctx context.Context, tx pgx.Tx, id int64, reason string) error {
	if reason == "" {
		return errors.New("matchmaking: cancel without a reason")
	}
	q := s.q.WithTx(tx)
	m, err := s.lockWait(ctx, q, id) // a forming match's holds are done or not started: none lands after the refund
	if err != nil {
		return err
	}
	switch m.Status {
	case "forming", "running":
		_, err = q.CancelMatch(ctx, store.CancelMatchParams{ID: id, Reason: reason})
		return err
	case "finishing", "settled":
		return ErrClosed
	}
	return nil
}

// lockWait locks match id in any status, waiting for a transaction advancing it.
func (s *Service) lockWait(ctx context.Context, q *store.Queries, id int64) (Match, error) {
	row, err := q.LockMatchWait(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Match{}, ErrNotFound
	}
	if err != nil {
		return Match{}, err
	}
	ms, err := s.toMatches(ctx, q, []store.MmMatch{row})
	if err != nil {
		return Match{}, err
	}
	return ms[0], nil
}

// teams is the number of teams of m (team indexes are 0..n-1).
func teams(m Match) int {
	n := 0
	for _, p := range m.Players {
		n = max(n, p.Team+1)
	}
	return n
}

type payout struct {
	UserID int64 `json:"user_id"`
	Amount int64 `json:"amount"`
}

type ratingChange struct {
	Mu      float64 `json:"mu"`
	Sigma   float64 `json:"sigma"`
	Ordinal float64 `json:"ordinal"`
}

// payouts is what a staked finishing match pays: refund when it is a draw the queue refunds,
// else the winners (zero amounts dropped): Payout's, or the best rank's players splitting the pot
// less the fee equally, the remainder one unit each in EntryID order.
func payouts(c QueueConfig, m Match) (wins []ledger.PotWin, refund bool, err error) {
	if m.Stake == 0 {
		return nil, false, nil
	}
	ranks := m.Result.Ranks
	best := slices.Min(ranks)
	if c.DrawRefund && slices.Max(ranks) == best {
		return nil, true, nil
	}
	pot := m.Stake * int64(len(m.Players))
	left := pot - ledger.FeeOf(pot, m.FeeBP)
	if c.Payout != nil {
		var sum int64
		for _, w := range c.Payout(pot, m, *m.Result) {
			if w.Amount < 0 {
				return nil, false, fmt.Errorf("matchmaking: match %d: Payout pays %d", m.ID, w.Amount)
			}
			sum += w.Amount
			if w.Amount > 0 {
				wins = append(wins, w)
			}
		}
		if sum != left {
			return nil, false, fmt.Errorf("matchmaking: match %d: Payout pays %d, want %d", m.ID, sum, left)
		}
	} else {
		var top []Player
		for _, p := range m.Players {
			if ranks[p.Team] == best {
				top = append(top, p)
			}
		}
		n := int64(len(top))
		for i, p := range top {
			amount := left / n
			if int64(i) < left%n {
				amount++
			}
			if amount > 0 {
				wins = append(wins, ledger.PotWin{UserID: p.UserID, Amount: amount})
			}
		}
	}
	if len(wins) > ledger.MaxPotWinners {
		return nil, false, fmt.Errorf("matchmaking: match %d: %w: %d", m.ID, ledger.ErrTooManyWinners, len(wins))
	}
	return wins, false, nil
}

// potEntries is every player's stake.
func potEntries(m Match) []ledger.PotEntry {
	entries := make([]ledger.PotEntry, len(m.Players))
	for i, p := range m.Players {
		entries[i] = ledger.PotEntry{ID: p.EntryID, UserID: p.UserID, Amount: m.Stake}
	}
	return entries
}

// settle pays out a finishing match under its row lock (tx), then, in tx, updates the ratings,
// marks it settled and calls OnFinish. An error rolls tx back: the match stays finishing and the
// next try repeats the ledger call (a no-op once it went through) and OnFinish (which must be
// idempotent). OnFinish runs holding the match's lock: it must not Finish or Cancel the same match.
func (s *Service) settle(ctx context.Context, tx pgx.Tx, m Match) error {
	queue, err := s.queueOf(m.Queue)
	if err != nil {
		return err
	}
	wins, refund, err := payouts(queue.cfg, m)
	if err != nil {
		return err
	}
	if m.Stake > 0 {
		if refund {
			err = s.a.Ledger.VoidPot(ctx, Game, m.Currency, potEntries(m))
		} else {
			_, err = s.a.Ledger.SettlePotFee(ctx, Game, m.Currency, m.ID, m.FeeBP, potEntries(m), wins)
		}
		if err != nil {
			return err
		}
	}
	q := s.q.WithTx(tx)
	ratings := map[int64]ratingChange{}
	if queue.cfg.Ranked {
		if ratings, err = rateMatch(ctx, q, queue.cfg.RatingKey, m); err != nil {
			return err
		}
	}
	if n, err := q.SetMatchSettled(ctx, m.ID); err != nil || n == 0 {
		return err
	}
	m.Status = "settled"
	if f := queue.cfg.OnFinish; f != nil {
		if err := f(ctx, m); err != nil {
			return fmt.Errorf("OnFinish: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	paid := []payout{}
	for _, w := range wins {
		paid = append(paid, payout{w.UserID, w.Amount})
	}
	for _, p := range m.Players {
		s.push(ctx, p.UserID, map[string]any{"type": "match.finished", "match": m, "payouts": paid, "ratings": ratings})
	}
	return nil
}

// rateMatch updates the players' ratings under key from m's result. Teams are rated from the
// ratings as they are now in mm_ratings (locked; DefaultRating if absent), not the formation
// snapshot in mm_match_players, so games played meanwhile under the same key all count.
func rateMatch(ctx context.Context, q *store.Queries, key string, m Match) (map[int64]ratingChange, error) {
	users := make([]int64, len(m.Players))
	for i, p := range m.Players {
		users[i] = p.UserID
	}
	slices.Sort(users)
	d := DefaultRating()
	if err := q.EnsureRatings(ctx, store.EnsureRatingsParams{Key: key, Mu: d.Mu, Sigma: d.Sigma, UserIds: users}); err != nil {
		return nil, err
	}
	rows, err := q.LockRatings(ctx, store.LockRatingsParams{Key: key, UserIds: users})
	if err != nil {
		return nil, err
	}
	cur := map[int64]Rating{}
	for _, r := range rows {
		cur[r.UserID] = Rating{r.Mu, r.Sigma}
	}
	byTeam := make([][]Player, len(m.Result.Ranks))
	for _, p := range m.Players {
		byTeam[p.Team] = append(byTeam[p.Team], p)
	}
	var who [][]Player
	var in [][]Rating
	var ranks []int
	for i, ps := range byTeam {
		if len(ps) == 0 {
			continue
		}
		rs := make([]Rating, len(ps))
		for j, p := range ps {
			rs[j] = cur[p.UserID]
		}
		who, in, ranks = append(who, ps), append(in, rs), append(ranks, m.Result.Ranks[i])
	}
	out := map[int64]ratingChange{}
	for i, rs := range rate(in, ranks) {
		for j, r := range rs {
			u := who[i][j].UserID
			if err := q.SetRating(ctx, store.SetRatingParams{UserID: u, Key: key, Mu: r.Mu, Sigma: r.Sigma}); err != nil {
				return nil, err
			}
			out[u] = ratingChange{r.Mu, r.Sigma, r.Ordinal()}
		}
	}
	return out, nil
}
