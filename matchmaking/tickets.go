package matchmaking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"

	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking/internal/store"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// QueueResult is the outcome of match.queue: exactly one field is set. A repeat of an idem_key
// returns the stored outcome, however long ago: the live ticket, the match it became, or why it
// was removed ("cancelled", "party_changed", ...). The key alone identifies the call: a repeat
// with a different payload gets the stored outcome too.
type QueueResult struct {
	Ticket  *Ticket `json:"ticket,omitempty"`
	MatchID int64   `json:"match_id,omitempty"`
	Removed string  `json:"removed,omitempty"`
}

type queueReq struct {
	Queue      string            `json:"queue"`
	Currency   ledger.Currency   `json:"currency"`
	Stake      int64             `json:"stake"`
	Properties map[string]string `json:"properties"`
	Numbers    map[string]int64  `json:"numbers"`
	IdemKey    string            `json:"idem_key"`
}

func (s *Service) handle() {
	r := s.a.Router
	r.Handle("match.queue", realtime.JSON(s.enqueue))
	r.Handle("match.cancel", realtime.JSON(func(ctx context.Context, u int64, _ struct{}) (struct{}, error) {
		return struct{}{}, s.cancel(ctx, u)
	}))
	r.Handle("match.status", realtime.JSON(func(ctx context.Context, u int64, _ struct{}) (status, error) {
		return s.status(ctx, u)
	}))
	r.Handle("rating.get", realtime.JSON(func(ctx context.Context, u int64, req struct {
		Key string `json:"key"`
	}) (ratingReply, error) {
		if req.Key == "" {
			return ratingReply{}, badPayload("key required")
		}
		r, n, err := s.rating(ctx, u, req.Key)
		return ratingReply{r.Mu, r.Sigma, r.Ordinal(), n}, err
	}))
}

func badPayload(msg string) error {
	return &realtime.ClientError{Code: realtime.CodeBadPayload, Message: msg}
}

type status struct {
	Ticket  *Ticket `json:"ticket"`
	Matches []Match `json:"matches"`
}

type ratingReply struct {
	Mu      float64 `json:"mu"`
	Sigma   float64 `json:"sigma"`
	Ordinal float64 `json:"ordinal"`
	Matches int     `json:"matches"`
}

// Rating is the user's rating under key; DefaultRating when they have no games under it.
func (s *Service) Rating(ctx context.Context, userID int64, key string) (Rating, error) {
	r, _, err := s.rating(ctx, userID, key)
	return r, err
}

func (s *Service) rating(ctx context.Context, userID int64, key string) (Rating, int, error) {
	row, err := s.q.GetRating(ctx, store.GetRatingParams{UserID: userID, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultRating(), 0, nil
	}
	return Rating{row.Mu, row.Sigma}, int(row.Matches), err
}

// members resolves who a queue call puts on the ticket. A user in no party, or only in parties
// of one, queues alone. Otherwise it is the multi-member party they lead (with PartiesPerUser > 1,
// the oldest such party); being only a member of one is party.ErrNotLeader.
func (s *Service) members(ctx context.Context, user int64) (partyID int64, users []int64, err error) {
	ps, err := s.parties.Of(ctx, user)
	if err != nil {
		return 0, nil, err
	}
	member := false
	for _, p := range ps {
		if len(p.Members) < 2 {
			continue
		}
		if p.LeaderID == user {
			return p.ID, slices.Sorted(slices.Values(p.Members)), nil
		}
		member = true
	}
	if member {
		return 0, nil, party.ErrNotLeader
	}
	return 0, []int64{user}, nil
}

// check validates the request against the queue's config.
func (c QueueConfig) check(r queueReq) (ledger.Currency, error) {
	if len(r.Properties) != len(c.Properties) || len(r.Numbers) != len(c.Numbers) {
		return "", gameconfig.ErrInvalid
	}
	for _, k := range c.Properties {
		if _, ok := r.Properties[k]; !ok {
			return "", gameconfig.ErrInvalid
		}
	}
	for k := range c.Numbers {
		if _, ok := r.Numbers[k]; !ok {
			return "", gameconfig.ErrInvalid
		}
	}
	if c.Stakes == nil {
		if r.Currency != "" || r.Stake != 0 {
			return "", gameconfig.ErrInvalid
		}
		return "", nil
	}
	if !slices.Contains(c.Stakes[r.Currency], r.Stake) {
		return "", gameconfig.ErrInvalid
	}
	cur, _, err := c.Game.Check(r.Currency, "", r.Stake)
	return cur, err
}

// fits reports whether n players fit one team of the queue.
func (c QueueConfig) fits(n int) bool {
	most := 1
	for _, t := range c.Teams {
		most = max(most, t.Max)
	}
	if c.FFA != nil {
		most = 1
	}
	return n <= most
}

func (s *Service) enqueue(ctx context.Context, user int64, r queueReq) (QueueResult, error) {
	if r.IdemKey == "" || len(r.IdemKey) > 64 {
		return QueueResult{}, badPayload("idem_key required, at most 64 bytes")
	}
	if res, ok, err := s.outcome(ctx, s.q, user, r.IdemKey); err != nil || ok {
		return res, err
	}
	s.mu.Lock()
	q := s.queues[r.Queue]
	s.mu.Unlock()
	if q == nil {
		return QueueResult{}, ErrUnknownQueue
	}
	cfg := q.cfg
	cur, err := cfg.check(r)
	if err != nil {
		return QueueResult{}, err
	}
	partyID, users, err := s.members(ctx, user)
	if err != nil {
		return QueueResult{}, err
	}
	if !cfg.fits(len(users)) {
		return QueueResult{}, ErrPartyTooBig
	}
	t := Ticket{Queue: cfg.Name, Leader: user, PartyID: partyID, Users: users, Currency: cur, Stake: r.Stake,
		Properties: r.Properties, Server: map[string]string{}, Numbers: r.Numbers}
	if t.Properties == nil {
		t.Properties = map[string]string{}
	}
	if t.Numbers == nil {
		t.Numbers = map[string]int64{}
	}
	if t.Rating, err = s.meanOrdinal(ctx, cfg.RatingKey, users); err != nil {
		return QueueResult{}, err
	}
	if cfg.Prepare != nil {
		if err := cfg.Prepare(ctx, &t); err != nil {
			return QueueResult{}, err
		}
	}
	props, _ := json.Marshal(t.Properties)
	server, _ := json.Marshal(t.Server)
	numbers, _ := json.Marshal(t.Numbers)

	var res QueueResult
	var replay bool
	err = pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) error {
		qs := s.q.WithTx(tx)
		replay = false
		n, err := qs.InsertRequest(ctx, store.InsertRequestParams{UserID: user, IdemKey: r.IdemKey, Queue: cfg.Name})
		if err != nil {
			return err
		}
		if n == 0 { // a concurrent call with this key committed first
			replay = true
			res, _, err = s.outcome(ctx, qs, user, r.IdemKey)
			return err
		}
		if !cfg.Concurrent {
			busy, err := qs.UsersBusy(ctx, store.UsersBusyParams{UserIds: users, Queue: cfg.Name})
			if err != nil {
				return err
			}
			if busy {
				return ErrBusy
			}
		}
		row, err := qs.InsertTicket(ctx, store.InsertTicketParams{Queue: cfg.Name, LeaderID: user, PartyID: partyID,
			Currency: string(cur), Stake: t.Stake, Properties: props, Server: server, Numbers: numbers, Rating: t.Rating})
		if err != nil {
			return err
		}
		t.ID, t.QueuedAt = row.ID, row.QueuedAt.Time
		for _, u := range users {
			if err := qs.InsertTicketUser(ctx, store.InsertTicketUserParams{UserID: u, TicketID: t.ID}); err != nil {
				var pg *pgconn.PgError
				if errors.As(err, &pg) && pg.Code == "23505" {
					return ErrQueued
				}
				return err
			}
		}
		if err := qs.SetRequestTicket(ctx, store.SetRequestTicketParams{UserID: user, IdemKey: r.IdemKey, TicketID: t.ID}); err != nil {
			return err
		}
		res = QueueResult{Ticket: &t}
		return nil
	})
	if err != nil || replay {
		return res, err
	}
	for _, u := range users {
		s.push(ctx, u, map[string]any{"type": "match.ticket", "ticket": t})
	}
	// The party may have changed between reading it and the commit, after its hook ran.
	if partyID != 0 {
		if p, err := s.parties.Get(ctx, partyID); err != nil || p.LeaderID != user ||
			!slices.Equal(slices.Sorted(slices.Values(p.Members)), users) {
			s.dropParty(ctx, partyID)
			res = QueueResult{Removed: "party_changed"}
		}
	}
	return res, nil
}

// outcome is the stored result of a request, if the key was used.
func (s *Service) outcome(ctx context.Context, q *store.Queries, user int64, key string) (QueueResult, bool, error) {
	r, err := q.GetRequest(ctx, store.GetRequestParams{UserID: user, IdemKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueResult{}, false, nil
	}
	if err != nil {
		return QueueResult{}, false, err
	}
	if r.TicketID != 0 {
		t, err := loadTicket(ctx, q, r.TicketID)
		if err == nil {
			return QueueResult{Ticket: &t}, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return QueueResult{}, false, err
		}
	}
	return QueueResult{MatchID: r.MatchID, Removed: r.Removed}, true, nil
}

func loadTicket(ctx context.Context, q *store.Queries, id int64) (Ticket, error) {
	r, err := q.GetTicket(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	users, err := q.TicketUsers(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	t := Ticket{ID: r.ID, Queue: r.Queue, Leader: r.LeaderID, PartyID: r.PartyID, Users: users,
		Currency: ledger.Currency(r.Currency), Stake: r.Stake, Rating: r.Rating, QueuedAt: r.QueuedAt.Time}
	for _, f := range []struct {
		raw []byte
		to  any
	}{{r.Properties, &t.Properties}, {r.Server, &t.Server}, {r.Numbers, &t.Numbers}} {
		if err := json.Unmarshal(f.raw, f.to); err != nil {
			return Ticket{}, err
		}
	}
	return t, nil
}

// meanOrdinal is the users' mean ordinal under key; users without games count as DefaultRating.
func (s *Service) meanOrdinal(ctx context.Context, key string, users []int64) (float64, error) {
	rows, err := s.q.UserRatings(ctx, store.UserRatingsParams{Key: key, UserIds: users})
	if err != nil {
		return 0, err
	}
	sum := float64(len(users)-len(rows)) * DefaultRating().Ordinal()
	for _, r := range rows {
		sum += Rating{r.Mu, r.Sigma}.Ordinal()
	}
	return sum / float64(len(users)), nil
}

func (s *Service) push(ctx context.Context, user int64, v any) {
	_ = realtime.PublishJSON(ctx, s.a.Realtime, realtime.UserChannel(user), v) // best effort: clients re-read with match.status
}

func (s *Service) cancel(ctx context.Context, user int64) error {
	id, err := s.q.UserTicketID(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.remove(ctx, id, "cancelled")
}

// remove deletes a ticket, records why on its request and tells every ticket user. A ticket that
// is already gone (matched or removed by another call) is left alone.
func (s *Service) remove(ctx context.Context, ticketID int64, reason string) error {
	var users []int64
	err := pgx.BeginFunc(ctx, s.a.Pool, func(tx pgx.Tx) (err error) {
		q := s.q.WithTx(tx)
		users = nil
		if users, err = q.TicketUsers(ctx, ticketID); err != nil {
			return err
		}
		n, err := q.DeleteTicket(ctx, ticketID)
		if err != nil || n == 0 {
			users = nil
			return err
		}
		return q.SetRequestRemoved(ctx, store.SetRequestRemovedParams{TicketID: ticketID, Removed: reason})
	})
	for _, u := range users {
		s.push(ctx, u, map[string]any{"type": "match.ticket_removed", "reason": reason})
	}
	return err
}

func (s *Service) onPartyChange(ctx context.Context, p party.Party) { s.dropParty(ctx, p.ID) }

// dropParty removes the party's ticket, if it is queued.
func (s *Service) dropParty(ctx context.Context, partyID int64) {
	ids, err := s.q.PartyTicketIDs(ctx, partyID)
	for _, id := range ids {
		if err = s.remove(ctx, id, "party_changed"); err != nil {
			break
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "matchmaking: dropping a changed party's ticket", "party", partyID, "err", err)
	}
}

func (s *Service) status(ctx context.Context, user int64) (status, error) {
	st := status{Matches: []Match{}}
	id, err := s.q.UserTicketID(ctx, user)
	if err == nil {
		t, err := loadTicket(ctx, s.q, id)
		if err == nil {
			st.Ticket = &t
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return st, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	rows, err := s.q.UserActiveMatches(ctx, user)
	if err != nil || len(rows) == 0 {
		return st, err
	}
	ids := make([]int64, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	ps, err := s.q.MatchPlayers(ctx, ids)
	if err != nil {
		return st, err
	}
	for _, m := range rows {
		out := Match{ID: m.ID, Queue: m.Queue, Ref: m.Ref, Status: m.Status, Currency: ledger.Currency(m.Currency),
			Stake: m.Stake, FeeBP: m.FeeBp, Reason: m.Reason, CreatedAt: m.CreatedAt.Time, Deadline: m.Deadline.Time,
			Players: []Player{}}
		if m.Result != nil {
			out.Result = &Result{}
			if err := json.Unmarshal(m.Result, out.Result); err != nil {
				return st, err
			}
		}
		for _, p := range ps {
			if p.MatchID == m.ID {
				out.Players = append(out.Players, Player{EntryID: p.ID, UserID: p.UserID, PartyID: p.PartyID, Team: int(p.Team)})
			}
		}
		st.Matches = append(st.Matches, out)
	}
	return st, nil
}
