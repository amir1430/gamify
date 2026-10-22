package matchmaking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// holdPot is the ledger hold; tests swap it for a failing one.
var holdPot = (*ledger.Ledger).HoldPot

func (s *Service) tick(ctx context.Context) {
	if err := s.MatchNow(ctx); err != nil {
		slog.ErrorContext(ctx, "matchmaking: tick", "err", err)
	}
}

// MatchNow runs one matcher pass over every queue (Register ticks it every Config.Tick): drop
// expired and offline tickets, form matches, hold their stakes, call OnMatch and start them. A
// queue another instance is matching is skipped. A match the ledger failed for stays forming and
// Reconcile finishes it.
func (s *Service) MatchNow(ctx context.Context) error {
	s.mu.Lock()
	qs := slices.Collect(maps.Values(s.queues))
	s.mu.Unlock()
	var errs []error
	for _, q := range qs {
		ids, err := s.formQueue(ctx, q)
		if err != nil {
			errs = append(errs, fmt.Errorf("matchmaking: queue %q: %w", q.cfg.Name, err))
		}
		for _, id := range ids {
			if err := s.advance(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("matchmaking: match %d: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

// Reconcile cancels running matches past their deadline (reason "timeout") and finishes matches a
// crash, a ledger outage or an admin action left forming, finishing or cancelling.
func (s *Service) Reconcile(ctx context.Context) {
	ids, err := s.q.TimedOutMatches(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "matchmaking: reconcile", "err", err)
	}
	for _, id := range ids {
		if err := s.Cancel(ctx, id, "timeout"); err != nil && !errors.Is(err, ErrClosed) { // ErrClosed: finished meanwhile
			slog.ErrorContext(ctx, "matchmaking: timeout", "match", id, "err", err)
		}
	}
	ids, err = s.q.UnfinishedMatches(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "matchmaking: reconcile", "err", err)
	}
	for _, id := range ids {
		if err := s.advance(ctx, id); err != nil {
			slog.ErrorContext(ctx, "matchmaking: reconcile", "match", id, "err", err)
		}
	}
}

// Get is the match with id (ErrNotFound if there is none).
func (s *Service) Get(ctx context.Context, id int64) (Match, error) {
	return s.getMatch(ctx, s.q, id)
}

func (s *Service) getMatch(ctx context.Context, q *store.Queries, id int64) (Match, error) {
	m, err := q.GetMatch(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Match{}, ErrNotFound
	}
	if err != nil {
		return Match{}, err
	}
	ms, err := s.toMatches(ctx, q, []store.MmMatch{m})
	if err != nil {
		return Match{}, err
	}
	return ms[0], nil
}

// toMatches adds the players to match rows.
func (s *Service) toMatches(ctx context.Context, q *store.Queries, rows []store.MmMatch) ([]Match, error) {
	ids := make([]int64, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	ps, err := q.MatchPlayers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Match, 0, len(rows))
	for _, m := range rows {
		mt := Match{ID: m.ID, Queue: m.Queue, Ref: m.Ref, Status: m.Status, Currency: ledger.Currency(m.Currency),
			Stake: m.Stake, FeeBP: m.FeeBp, Reason: m.Reason, CreatedAt: m.CreatedAt.Time, Deadline: m.Deadline.Time,
			Players: []Player{}}
		if m.Result != nil {
			mt.Result = &Result{}
			if err := json.Unmarshal(m.Result, mt.Result); err != nil {
				return nil, err
			}
		}
		for _, p := range ps {
			if p.MatchID == m.ID {
				mt.Players = append(mt.Players, Player{EntryID: p.ID, UserID: p.UserID, PartyID: p.PartyID, Team: int(p.Team)})
			}
		}
		out = append(out, mt)
	}
	return out, nil
}

type removal struct {
	users  []int64
	reason string
}

// formQueue drops the queue's expired and offline tickets and turns the rest into forming matches,
// in one transaction under the queue's advisory lock (none: another instance has it, skip).
func (s *Service) formQueue(ctx context.Context, q *Queue) (ids []int64, err error) {
	cfg := q.cfg
	var online map[int64]int
	if !cfg.AllowOffline {
		if online, err = s.a.Realtime.Online(ctx); err != nil {
			return nil, err
		}
	}
	var removed []removal
	err = pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error {
		ids, removed = nil, nil
		qs := s.q.WithTx(tx)
		if ok, err := qs.TryQueueLock(ctx, cfg.Name); err != nil || !ok {
			return err
		}
		rows, err := qs.QueueTickets(ctx, cfg.Name)
		if err != nil || len(rows) == 0 {
			return err
		}
		tickets, err := withUsers(ctx, qs, rows)
		if err != nil {
			return err
		}
		now := time.Now()
		var live []Ticket
		for _, t := range tickets {
			reason := ""
			switch {
			case cfg.MaxWait != Forever && now.Sub(t.QueuedAt) > cfg.MaxWait:
				reason = "expired"
			case !cfg.AllowOffline && slices.ContainsFunc(t.Users, func(u int64) bool { return online[u] == 0 }):
				reason = "offline"
			}
			if reason == "" {
				live = append(live, t)
				continue
			}
			users, err := removeTicket(ctx, qs, t.ID, reason)
			if err != nil {
				return err
			}
			removed = append(removed, removal{users, reason})
		}
		for _, f := range form(q, live, now) {
			id, err := s.insertMatch(ctx, qs, cfg, live, f, now)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, r := range removed {
		for _, u := range r.users {
			s.push(ctx, u, map[string]any{"type": "match.ticket_removed", "reason": r.reason})
		}
	}
	return ids, nil
}

// withUsers loads the tickets' users.
func withUsers(ctx context.Context, q *store.Queries, rows []store.MmTicket) ([]Ticket, error) {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	tus, err := q.TicketsUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	users := map[int64][]int64{}
	for _, tu := range tus {
		users[tu.TicketID] = append(users[tu.TicketID], tu.UserID)
	}
	out := make([]Ticket, len(rows))
	for i, r := range rows {
		if out[i], err = toTicket(r, users[r.ID]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// insertMatch records a formed match: the match (forming, with its tickets for a re-queue), its
// players in team order then user order (EntryID = row ID) with their ratings, and the requests
// pointed at it; its tickets are deleted.
func (s *Service) insertMatch(ctx context.Context, q *store.Queries, cfg QueueConfig, live []Ticket, f formed, now time.Time) (int64, error) {
	var snap []Ticket
	party := map[int64]int64{}
	var users []int64
	for _, id := range f.Tickets {
		t := live[slices.IndexFunc(live, func(t Ticket) bool { return t.ID == id })]
		snap = append(snap, t)
		for _, u := range t.Users {
			party[u] = t.PartyID
		}
		users = append(users, t.Users...)
	}
	rs, err := q.UserRatings(ctx, store.UserRatingsParams{Key: cfg.RatingKey, UserIds: users})
	if err != nil {
		return 0, err
	}
	rating := map[int64]Rating{}
	for _, r := range rs {
		rating[r.UserID] = Rating{r.Mu, r.Sigma}
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return 0, err
	}
	feeBP := cfg.FeeBP
	if cfg.NoFee {
		feeBP = 0
	}
	id, err := q.InsertMatch(ctx, store.InsertMatchParams{Queue: cfg.Name, Ref: fmt.Sprintf("ticket:%d", snap[0].ID),
		Currency: string(snap[0].Currency), Stake: snap[0].Stake, FeeBp: feeBP,
		Deadline: pgtype.Timestamptz{Time: now.Add(cfg.MatchTimeout), Valid: true}, Tickets: raw})
	if err != nil {
		return 0, err
	}
	for team, us := range f.Teams {
		for _, u := range slices.Sorted(slices.Values(us)) {
			r, ok := rating[u]
			if !ok {
				r = DefaultRating()
			}
			if err := q.InsertMatchPlayer(ctx, store.InsertMatchPlayerParams{MatchID: id, UserID: u, Team: int32(team),
				PartyID: party[u], RatingMu: r.Mu, RatingSigma: r.Sigma}); err != nil {
				return 0, err
			}
		}
	}
	for _, t := range f.Tickets {
		if _, err := q.DeleteTicket(ctx, t); err != nil { // locked by QueueTickets: still there
			return 0, err
		}
	}
	return id, q.SetRequestsMatch(ctx, store.SetRequestsMatchParams{MatchID: id, TicketIds: f.Tickets})
}

// queueOf is the registered queue called name.
func (s *Service) queueOf(name string) (*Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.queues[name]; q != nil {
		return q, nil
	}
	return nil, fmt.Errorf("matchmaking: queue %q not registered", name)
}

// advance takes a forming match through its holds, OnMatch and running, a finishing one through
// its settlement to settled, or a cancelling one through its refunds to cancelled. Every step is idempotent and the ledger steps run under the
// match's row lock, so the tick and the reconciler (on any instance) may advance the same match at
// once: a hold never lands after the refund. OnMatch runs outside the lock and may run again.
func (s *Service) advance(ctx context.Context, id int64) error {
	for {
		again, err := s.step(ctx, id)
		if err != nil || !again {
			return err
		}
	}
}

// step does one locked step; again is true when the match moved to a status with more to do.
func (s *Service) step(ctx context.Context, id int64) (again bool, err error) {
	tx, err := s.a.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)
	row, err := q.LockMatch(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) { // done, or another transaction is on it
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ms, err := s.toMatches(ctx, q, []store.MmMatch{row})
	if err != nil {
		return false, err
	}
	m := ms[0]
	switch m.Status {
	case "cancelling":
		return false, s.cancelled(ctx, tx, m, row.Tickets)
	case "finishing":
		return false, s.settle(ctx, tx, m)
	}

	if m.Stake > 0 {
		var short []int64
		for _, p := range m.Players {
			err := holdPot(s.a.Ledger, ctx, Game, m.Currency, ledger.PotEntry{ID: p.EntryID, UserID: p.UserID, Amount: m.Stake})
			switch {
			case errors.Is(err, ledger.ErrInsufficientFunds), errors.Is(err, ledger.ErrAlreadyFailed):
				short = append(short, p.UserID)
			case err != nil: // transient: stays forming for the reconciler
				return false, err
			}
		}
		if len(short) > 0 {
			return true, s.broke(ctx, tx, m, row.Tickets, short)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	queue, err := s.queueOf(m.Queue)
	if err != nil {
		return false, err
	}
	if f := queue.cfg.OnMatch; f != nil {
		if err := f(ctx, m); err != nil {
			slog.WarnContext(ctx, "matchmaking: OnMatch failed, cancelling", "match", id, "err", err)
			_, err := s.q.SetMatchCancelling(ctx, store.SetMatchCancellingParams{ID: id, Reason: "on_match", Tickets: []byte("[]")})
			return true, err
		}
	}
	if n, err := s.q.SetMatchRunning(ctx, id); err != nil || n == 0 {
		return false, err
	}
	if m, err = s.Get(ctx, id); err != nil {
		return false, err
	}
	for _, p := range m.Players {
		s.push(ctx, p.UserID, map[string]any{"type": "match.found", "match": m})
	}
	return false, nil
}

// broke records a refused hold: the match is cancelling with the units to re-queue (every ticket
// without a short user), and the short units' tickets are removed (insufficient_funds).
func (s *Service) broke(ctx context.Context, tx pgx.Tx, m Match, raw []byte, short []int64) error {
	var tickets, keep []Ticket
	if err := json.Unmarshal(raw, &tickets); err != nil {
		return err
	}
	var drop []Ticket
	for _, t := range tickets {
		if slices.ContainsFunc(t.Users, func(u int64) bool { return slices.Contains(short, u) }) {
			drop = append(drop, t)
		} else {
			keep = append(keep, t)
		}
	}
	keepRaw, err := json.Marshal(keep)
	if err != nil {
		return err
	}
	q := s.q.WithTx(tx)
	if _, err := q.SetMatchCancelling(ctx, store.SetMatchCancellingParams{ID: m.ID, Reason: "insufficient_funds", Tickets: keepRaw}); err != nil {
		return err
	}
	for _, t := range drop {
		if err := q.SetRequestRemoved(ctx, store.SetRequestRemovedParams{TicketID: t.ID, Removed: "insufficient_funds"}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, t := range drop {
		for _, u := range t.Users {
			s.push(ctx, u, map[string]any{"type": "match.ticket_removed", "reason": "insufficient_funds"})
		}
	}
	return nil
}

// cancelled refunds a cancelling match, marks it cancelled and re-queues its kept tickets with
// their original queue time. Without tickets to re-queue, the players get match.cancelled.
func (s *Service) cancelled(ctx context.Context, tx pgx.Tx, m Match, raw []byte) error {
	if m.Stake > 0 {
		if err := s.a.Ledger.VoidPot(ctx, Game, m.Currency, potEntries(m)); err != nil {
			return err
		}
	}
	var tickets []Ticket
	if err := json.Unmarshal(raw, &tickets); err != nil {
		return err
	}
	q := s.q.WithTx(tx)
	if _, err := q.SetMatchCancelled(ctx, m.ID); err != nil {
		return err
	}
	var requeued []Ticket
	for _, t := range tickets {
		nt, ok, err := requeue(ctx, tx, s.q, t)
		if err != nil {
			return err
		}
		if ok {
			requeued = append(requeued, nt)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, t := range requeued {
		s.queued(ctx, t)
	}
	if m.Reason != "insufficient_funds" {
		m.Status = "cancelled"
		for _, p := range m.Players {
			s.push(ctx, p.UserID, map[string]any{"type": "match.cancelled", "match": m, "reason": m.Reason})
		}
	}
	return nil
}

// requeue inserts t again under a new ID with its original queue time and moves its requests to
// it. A user who queued again meanwhile (a Concurrent queue) keeps that ticket: t is dropped
// (already_queued).
func requeue(ctx context.Context, tx pgx.Tx, qs *store.Queries, t Ticket) (Ticket, bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return t, false, err
	}
	defer sp.Rollback(ctx)
	q := qs.WithTx(sp)
	props, _ := json.Marshal(t.Properties)
	server, _ := json.Marshal(t.Server)
	numbers, _ := json.Marshal(t.Numbers)
	id, err := q.RequeueTicket(ctx, store.RequeueTicketParams{Queue: t.Queue, LeaderID: t.Leader, PartyID: t.PartyID,
		Currency: string(t.Currency), Stake: t.Stake, Properties: props, Server: server, Numbers: numbers, Rating: t.Rating,
		QueuedAt: pgtype.Timestamptz{Time: t.QueuedAt, Valid: true}})
	if err != nil {
		return t, false, err
	}
	for _, u := range t.Users {
		if err := q.InsertTicketUser(ctx, store.InsertTicketUserParams{UserID: u, TicketID: id}); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23505" {
				if err := sp.Rollback(ctx); err != nil {
					return t, false, err
				}
				return t, false, qs.WithTx(tx).SetRequestRemoved(ctx, store.SetRequestRemovedParams{TicketID: t.ID, Removed: "already_queued"})
			}
			return t, false, err
		}
	}
	if err := q.MoveRequests(ctx, store.MoveRequestsParams{NewID: id, OldID: t.ID}); err != nil {
		return t, false, err
	}
	t.ID = id
	return t, true, sp.Commit(ctx)
}
