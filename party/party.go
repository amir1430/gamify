// Package party is an opt-in module: groups of players with a leader, join requests, chat and a
// lock other modules (matchmaking, tournaments) hold while the party is committed to something.
// It owns its tables (goose table goose_party); matchmaking reads parties through Service.
package party

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/amir1430/gamify/party/internal/store"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInParty     = &realtime.ClientError{Code: 1020, Message: "already in a party"}
	ErrNotFound    = &realtime.ClientError{Code: 1021, Message: "party not found"}
	ErrFull        = &realtime.ClientError{Code: 1022, Message: "party is full"}
	ErrNotLeader   = &realtime.ClientError{Code: 1023, Message: "not the party leader"}
	ErrLocked      = &realtime.ClientError{Code: 1024, Message: "party is locked"}
	ErrRateLimited = &realtime.ClientError{Code: 1025, Message: "too many messages"}
	ErrNotMember   = &realtime.ClientError{Code: 1026, Message: "not a party member"}
)

// Config's zero fields take the defaults.
type Config struct {
	PartiesPerUser int // default 1
	MaxSize        int // hard cap for party.create max, default 16
	MessageBytes   int // default 4096
	MessagesPerSec int // per user, default 5
}

func (c Config) withDefaults() Config {
	for _, f := range []struct {
		v *int
		d int
	}{{&c.PartiesPerUser, 1}, {&c.MaxSize, 16}, {&c.MessageBytes, 4096}, {&c.MessagesPerSec, 5}} {
		if *f.v <= 0 {
			*f.v = f.d
		}
	}
	return c
}

type Party struct {
	ID       int64   `json:"id"`
	LeaderID int64   `json:"leader_id"`
	Name     string  `json:"name"`
	Max      int     `json:"max"`
	Open     bool    `json:"open"`
	Locked   string  `json:"locked"`  // "" or who locked it ("tournament:7")
	Members  []int64 `json:"members"` // join order; Members[0] is the oldest
}

type Service struct {
	pool *pgxpool.Pool
	q    *store.Queries
	rt   realtime.Transport
	cfg  Config

	mu    sync.Mutex
	hooks []func(context.Context, Party)
	sent  map[int64][]time.Time // ponytail: per-instance limit, a shared limiter if abuse spans instances
}

func newService(pool *pgxpool.Pool, rt realtime.Transport, cfg Config) *Service {
	return &Service{pool: pool, q: store.New(pool), rt: rt, cfg: cfg.withDefaults(), sent: map[int64][]time.Time{}}
}

// OnChange registers f, called after the transaction commits whenever a party's membership or
// leader changes (create, join, accept, leave, kick, lead). A party whose last member left is
// passed with no Members. Register hooks at startup, before serving.
func (s *Service) OnChange(f func(ctx context.Context, p Party)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, f)
}

func load(ctx context.Context, q *store.Queries, id int64, forUpdate bool) (Party, error) {
	var r store.GetPartyRow
	var err error
	if forUpdate {
		var l store.LockPartyRow
		l, err = q.LockParty(ctx, id)
		r = store.GetPartyRow(l)
	} else {
		r, err = q.GetParty(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	if err != nil {
		return Party{}, err
	}
	m, err := q.PartyMembers(ctx, id)
	if err != nil {
		return Party{}, err
	}
	return Party{ID: r.ID, LeaderID: r.LeaderID, Name: r.Name, Max: int(r.MaxSize), Open: r.Open,
		Locked: r.LockedBy, Members: append([]int64{}, m...)}, nil
}

func (s *Service) Get(ctx context.Context, partyID int64) (Party, error) {
	return load(ctx, s.q, partyID, false)
}

// Of lists the parties the user is in, oldest party first.
func (s *Service) Of(ctx context.Context, userID int64) ([]Party, error) {
	ids, err := s.q.UserPartyIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []Party{}
	for _, id := range ids {
		p, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// List lists parties newest first (before 0: the first page), or only those userID is in when it
// is not 0.
func (s *Service) List(ctx context.Context, userID, before int64, limit int32) ([]Party, error) {
	rows, err := s.q.ListParties(ctx, store.ListPartiesParams{BeforeID: before, UserID: userID, MaxRows: limit})
	if err != nil {
		return nil, err
	}
	out := []Party{}
	for _, r := range rows {
		m, err := s.q.PartyMembers(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Party{ID: r.ID, LeaderID: r.LeaderID, Name: r.Name, Max: int(r.MaxSize), Open: r.Open,
			Locked: r.LockedBy, Members: append([]int64{}, m...)})
	}
	return out, nil
}

// Lock freezes membership (join, leave, kick, accept) on behalf of by, e.g. "tournament:7".
// Idempotent for the same by; ErrLocked if someone else holds it.
func (s *Service) Lock(ctx context.Context, partyID int64, by string) error {
	return s.setLock(ctx, partyID, by, true)
}

// Unlock releases a lock held by by; a party that isn't locked is left alone, one locked by
// someone else is ErrLocked.
func (s *Service) Unlock(ctx context.Context, partyID int64, by string) error {
	return s.setLock(ctx, partyID, by, false)
}

func (s *Service) setLock(ctx context.Context, partyID int64, by string, lock bool) error {
	var p Party
	err := s.tx(ctx, func(q *store.Queries) (err error) {
		if p, err = load(ctx, q, partyID, true); err != nil {
			return err
		}
		if p.Locked != "" && p.Locked != by {
			return ErrLocked
		}
		if lock == (p.Locked != "") {
			return nil
		}
		to := ""
		if lock {
			to = by
		}
		p.Locked = to
		return q.SetLocked(ctx, store.SetLockedParams{ID: partyID, Column2: to})
	})
	if err != nil {
		return err
	}
	s.push(ctx, p)
	return nil
}

func (s *Service) tx(ctx context.Context, f func(q *store.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return f(s.q.WithTx(tx)) })
}

type pushParty struct {
	Type  string `json:"type"`
	Party Party  `json:"party"`
	Left  bool   `json:"left,omitempty"`
}

// push sends the party to every member, and to each of left with left:true.
func (s *Service) push(ctx context.Context, p Party, left ...int64) {
	for _, u := range p.Members {
		s.send(ctx, u, pushParty{Type: "party", Party: p})
	}
	for _, u := range left {
		s.send(ctx, u, pushParty{Type: "party", Party: p, Left: true})
	}
}

func (s *Service) send(ctx context.Context, user int64, v any) {
	_ = realtime.PublishJSON(ctx, s.rt, realtime.UserChannel(user), v) // best effort: clients re-read with party.get
}

// changed announces a committed membership change: pushes, then the hooks. Both run on a context
// the caller hanging up doesn't cancel: the change is done, its consequences must follow.
func (s *Service) changed(ctx context.Context, p Party, left ...int64) {
	ctx = context.WithoutCancel(ctx)
	s.push(ctx, p, left...)
	s.mu.Lock()
	hooks := slices.Clone(s.hooks)
	s.mu.Unlock()
	for _, f := range hooks {
		f(ctx, p)
	}
}

// allow is a sliding one-second window per user.
func (s *Service) allow(user int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	w := s.sent[user]
	for len(w) > 0 && now.Sub(w[0]) >= time.Second {
		w = w[1:]
	}
	if len(w) >= s.cfg.MessagesPerSec {
		s.sent[user] = w
		return false
	}
	s.sent[user] = append(w, now)
	return true
}

// Disband deletes party id with its members and requests in the caller's transaction (an admin
// action; a locked party too) and returns it as it was. Nothing is announced yet: call Disbanded
// after the transaction commits.
func (s *Service) Disband(ctx context.Context, tx pgx.Tx, id int64) (Party, error) {
	q := s.q.WithTx(tx)
	p, err := load(ctx, q, id, true)
	if err != nil {
		return Party{}, err
	}
	return p, q.DeleteParty(ctx, id)
}

// Disbanded announces a party Disband deleted, once its transaction committed: every former member
// gets the party with "left":true and the OnChange hooks get it with no Members.
func (s *Service) Disbanded(ctx context.Context, p Party) {
	former := p.Members
	p.Members = []int64{}
	s.changed(ctx, p, former...)
}
