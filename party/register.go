package party

import (
	"context"
	"embed"
	"encoding/json"
	"slices"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/party/internal/store"
	"github.com/amir1430/gamify/realtime"
)

// Migrations is the module's tables (goose table goose_party).
//
//go:embed migrations/*.sql
var Migrations embed.FS

type partyReq struct {
	PartyID int64 `json:"party_id"` // optional when the caller is in one party
	UserID  int64 `json:"user_id"`
}

// Register adds parties to an app: its tables and the party.* RPCs.
func Register(ctx context.Context, a *app.App, cfg Config) (*Service, error) {
	if err := a.Migrate(ctx, Migrations, "goose_party"); err != nil {
		return nil, err
	}
	s := newService(a.Pool, a.Realtime, cfg)
	h := func(name string, f func(ctx context.Context, userID int64, r partyReq) (Party, error)) {
		a.Router.Handle(name, realtime.JSON(f))
	}
	a.Router.Handle("party.create", realtime.JSON(func(ctx context.Context, u int64, r struct {
		Max  int    `json:"max"`
		Name string `json:"name"`
		Open bool   `json:"open"`
	}) (Party, error) {
		return s.create(ctx, u, r.Max, r.Name, r.Open)
	}))
	h("party.join", func(ctx context.Context, u int64, r partyReq) (Party, error) { return s.join(ctx, u, r.PartyID) })
	h("party.accept", func(ctx context.Context, u int64, r partyReq) (Party, error) { return s.accept(ctx, u, r) })
	h("party.leave", func(ctx context.Context, u int64, r partyReq) (Party, error) { return s.leave(ctx, u, r.PartyID) })
	h("party.kick", func(ctx context.Context, u int64, r partyReq) (Party, error) { return s.kick(ctx, u, r) })
	h("party.lead", func(ctx context.Context, u int64, r partyReq) (Party, error) { return s.lead(ctx, u, r) })
	h("party.get", func(ctx context.Context, _ int64, r partyReq) (Party, error) { return s.Get(ctx, r.PartyID) })
	a.Router.Handle("party.send", realtime.JSON(func(ctx context.Context, u int64, r struct {
		PartyID int64           `json:"party_id"`
		Kind    string          `json:"kind"`
		Data    json.RawMessage `json:"data"`
	}) (struct{}, error) {
		return struct{}{}, s.message(ctx, u, r.PartyID, r.Kind, r.Data)
	}))
	return s, nil
}

func badPayload(msg string) error {
	return &realtime.ClientError{Code: realtime.CodeBadPayload, Message: msg}
}

func (s *Service) create(ctx context.Context, user int64, max int, name string, open bool) (Party, error) {
	if max <= 0 || max > s.cfg.MaxSize {
		max = s.cfg.MaxSize
	}
	var p Party
	err := s.tx(ctx, func(q *store.Queries) error {
		if err := s.checkRoom(ctx, q, user); err != nil {
			return err
		}
		id, err := q.CreateParty(ctx, store.CreatePartyParams{LeaderID: user, Name: name, MaxSize: int32(max), Open: open})
		if err != nil {
			return err
		}
		if err := q.AddMember(ctx, store.AddMemberParams{PartyID: id, UserID: user}); err != nil {
			return err
		}
		p, err = load(ctx, q, id, false)
		return err
	})
	if err != nil {
		return Party{}, err
	}
	s.changed(ctx, p)
	return p, nil
}

// checkRoom takes the per-user lock and enforces PartiesPerUser.
func (s *Service) checkRoom(ctx context.Context, q *store.Queries, user int64) error {
	if err := q.LockUserParties(ctx, int32(user)); err != nil {
		return err
	}
	ids, err := q.UserPartyIDs(ctx, user)
	if err != nil {
		return err
	}
	if len(ids) >= s.cfg.PartiesPerUser {
		return ErrInParty
	}
	return nil
}

func (s *Service) join(ctx context.Context, user, partyID int64) (Party, error) {
	var p Party
	var requested, joined bool
	err := s.tx(ctx, func(q *store.Queries) (err error) {
		requested, joined = false, false
		if err := q.LockUserParties(ctx, int32(user)); err != nil {
			return err
		}
		if p, err = load(ctx, q, partyID, true); err != nil {
			return err
		}
		if slices.Contains(p.Members, user) {
			return nil
		}
		if p.Locked != "" {
			return ErrLocked
		}
		if err := s.checkRoom(ctx, q, user); err != nil {
			return err
		}
		if len(p.Members) >= p.Max {
			return ErrFull
		}
		if !p.Open { // a repeat leaves the pending request alone and tells the leader once
			n, err := q.AddRequest(ctx, store.AddRequestParams{PartyID: partyID, UserID: user})
			requested = n > 0
			return err
		}
		joined = true
		if err = q.AddMember(ctx, store.AddMemberParams{PartyID: partyID, UserID: user}); err != nil {
			return err
		}
		p, err = load(ctx, q, partyID, false)
		return err
	})
	if err != nil {
		return Party{}, err
	}
	if requested {
		s.send(ctx, p.LeaderID, map[string]any{"type": "party.request", "party_id": partyID, "user_id": user})
	}
	if joined {
		s.changed(ctx, p)
	}
	return p, nil
}

// mine resolves the party an RPC acts on: partyID, or the caller's own (preferring one they lead).
// With none, notIn is the error.
func (s *Service) mine(ctx context.Context, user, partyID int64, notIn error) (int64, error) {
	if partyID != 0 {
		return partyID, nil
	}
	ps, err := s.Of(ctx, user)
	if err != nil {
		return 0, err
	}
	if len(ps) == 0 {
		return 0, notIn
	}
	for _, p := range ps {
		if p.LeaderID == user {
			return p.ID, nil
		}
	}
	return ps[0].ID, nil
}

// leaderAct locks the caller's party and runs f as its leader; the party is then reloaded.
// Lock order everywhere: a user's advisory lock (lockUser, if not 0) before the party row.
func (s *Service) leaderAct(ctx context.Context, user, partyID, lockUser int64, f func(q *store.Queries, p Party) error) (Party, error) {
	id, err := s.mine(ctx, user, partyID, ErrNotLeader)
	if err != nil {
		return Party{}, err
	}
	var p Party
	err = s.tx(ctx, func(q *store.Queries) (err error) {
		if lockUser != 0 {
			if err = q.LockUserParties(ctx, int32(lockUser)); err != nil {
				return err
			}
		}
		if p, err = load(ctx, q, id, true); err != nil {
			return err
		}
		if p.LeaderID != user {
			return ErrNotLeader
		}
		if err = f(q, p); err != nil {
			return err
		}
		p, err = load(ctx, q, id, false)
		return err
	})
	return p, err
}

func (s *Service) accept(ctx context.Context, user int64, r partyReq) (Party, error) {
	p, err := s.leaderAct(ctx, user, r.PartyID, r.UserID, func(q *store.Queries, p Party) error {
		if p.Locked != "" {
			return ErrLocked
		}
		n, err := q.DeleteRequest(ctx, store.DeleteRequestParams{PartyID: p.ID, UserID: r.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotMember
		}
		if err := s.checkRoom(ctx, q, r.UserID); err != nil {
			return err
		}
		if len(p.Members) >= p.Max {
			return ErrFull
		}
		return q.AddMember(ctx, store.AddMemberParams{PartyID: p.ID, UserID: r.UserID})
	})
	if err != nil {
		return Party{}, err
	}
	s.changed(ctx, p)
	return p, nil
}

func (s *Service) kick(ctx context.Context, user int64, r partyReq) (Party, error) {
	p, err := s.leaderAct(ctx, user, r.PartyID, 0, func(q *store.Queries, p Party) error {
		if p.Locked != "" {
			return ErrLocked
		}
		if r.UserID == user {
			return badPayload("use party.leave")
		}
		n, err := q.RemoveMember(ctx, store.RemoveMemberParams{PartyID: p.ID, UserID: r.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotMember
		}
		return nil
	})
	if err != nil {
		return Party{}, err
	}
	s.changed(ctx, p, r.UserID)
	return p, nil
}

func (s *Service) lead(ctx context.Context, user int64, r partyReq) (Party, error) {
	p, err := s.leaderAct(ctx, user, r.PartyID, 0, func(q *store.Queries, p Party) error {
		if !slices.Contains(p.Members, r.UserID) {
			return ErrNotMember
		}
		return q.SetLeader(ctx, store.SetLeaderParams{ID: p.ID, LeaderID: r.UserID})
	})
	if err != nil {
		return Party{}, err
	}
	s.changed(ctx, p)
	return p, nil
}

// leave returns the party as the leaver left it (Members without them).
func (s *Service) leave(ctx context.Context, user, partyID int64) (Party, error) {
	id, err := s.mine(ctx, user, partyID, ErrNotMember)
	if err != nil {
		return Party{}, err
	}
	var p Party
	err = s.tx(ctx, func(q *store.Queries) (err error) {
		if p, err = load(ctx, q, id, true); err != nil {
			return err
		}
		if !slices.Contains(p.Members, user) {
			return ErrNotMember
		}
		if p.Locked != "" {
			return ErrLocked
		}
		if _, err = q.RemoveMember(ctx, store.RemoveMemberParams{PartyID: id, UserID: user}); err != nil {
			return err
		}
		if p.Members = slices.DeleteFunc(p.Members, func(m int64) bool { return m == user }); len(p.Members) == 0 {
			return q.DeleteParty(ctx, id)
		}
		if p.LeaderID == user {
			p.LeaderID = p.Members[0] // oldest remaining
			return q.SetLeader(ctx, store.SetLeaderParams{ID: id, LeaderID: p.LeaderID})
		}
		return nil
	})
	if err != nil {
		return Party{}, err
	}
	s.changed(ctx, p, user)
	return p, nil
}

func (s *Service) message(ctx context.Context, user, partyID int64, kind string, data json.RawMessage) error {
	if kind != "chat" && kind != "data" {
		return badPayload("kind must be chat or data")
	}
	p, err := s.Get(ctx, partyID)
	if err != nil {
		return err
	}
	if !slices.Contains(p.Members, user) {
		return ErrNotMember
	}
	if len(data) > s.cfg.MessageBytes {
		return gameconfig.ErrInvalid
	}
	if !s.allow(user) {
		return ErrRateLimited
	}
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	msg := struct {
		Type    string          `json:"type"`
		PartyID int64           `json:"party_id"`
		From    int64           `json:"from"`
		Kind    string          `json:"kind"`
		Data    json.RawMessage `json:"data"`
	}{"party.message", partyID, user, kind, data}
	for _, m := range p.Members {
		s.send(ctx, m, msg)
	}
	return nil
}
