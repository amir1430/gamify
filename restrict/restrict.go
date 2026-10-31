// Package restrict is the opt-in module for per-player restrictions set by operators: a suspended
// player can't log in and every RPC is refused; a block refuses one group of RPC methods the brand
// names (its "bets", "trades", "chat"), so the player can still finish what is in play. Each lasts
// until lifted or until a time. Login and the RPC router check an in-memory copy, reloaded every
// ReloadEvery, so checks never wait on Postgres.
package restrict

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/amir1430/gamify/realtime"
	"github.com/amir1430/gamify/restrict/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrSuspended = &realtime.ClientError{Code: 1108, Message: "this account is suspended"}
	ErrBlocked   = &realtime.ClientError{Code: 1108, Message: "this action is restricted on your account"}
	// ErrUnknownBlock: a Restriction names a block the Config doesn't have.
	ErrUnknownBlock = errors.New("restrict: unknown block")
)

// Config is the brand's policy; the zero value only allows suspending.
type Config struct {
	// Blocks names groups of RPC methods an operator can block for one player, e.g.
	// {"bets": {"dice.bet", "mines.start"}, "trades": {"shop.buy"}}. A method may be in several.
	// An entry "method?field=value" matches only calls whose JSON payload has that top-level field
	// equal to value (a string, or a number or bool as written), e.g. "duel.create?game=chess": a
	// call without the field doesn't match, so the brand must require it on such a method.
	Blocks map[string][]string
	// ReloadEvery is how soon a change made on another instance applies here (default 5s).
	ReloadEvery time.Duration
}

// Restriction is one player's stored row. The zero value restricts nothing.
type Restriction struct {
	Suspended bool       `json:"suspended"`
	Blocks    []string   `json:"blocks"` // names from Config.Blocks
	Note      string     `json:"note"`
	Until     *time.Time `json:"until"` // nil: until lifted
	Version   int64      `json:"version"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// Any reports whether r restricts anything, ignoring Until.
func (r Restriction) Any() bool { return r.Suspended || len(r.Blocks) > 0 }

// Active reports whether r restricts anything at now.
func (r Restriction) Active(now time.Time) bool {
	return r.Any() && (r.Until == nil || now.Before(*r.Until))
}

func fromRow(row store.UserRestriction) Restriction {
	r := Restriction{Suspended: row.Suspended, Blocks: row.Blocks, Note: row.Note, Version: row.Version, UpdatedAt: &row.UpdatedAt.Time}
	if r.Blocks == nil {
		r.Blocks = []string{}
	}
	if row.Until.Valid {
		r.Until = &row.Until.Time
	}
	return r
}

// rule is one Blocks entry of a method: its block, and the payload field it needs ("" = any call).
type rule struct{ block, field, value string }

// parseEntry splits "method" or "method?field=value".
func parseEntry(e string) (method string, r rule, ok bool) {
	method, cond, hasCond := strings.Cut(e, "?")
	if method == "" {
		return "", r, false
	}
	if hasCond {
		r.field, r.value, ok = strings.Cut(cond, "=")
		if !ok || r.field == "" || r.value == "" {
			return "", r, false
		}
	}
	return method, r, true
}

// matches reports whether data's top-level field equals the rule's value.
func (r rule) matches(data []byte) bool {
	if r.field == "" {
		return true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	raw, ok := m[r.field]
	if !ok {
		return false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == r.value
	}
	return string(raw) == r.value // a number or bool as written
}

type Service struct {
	q       *store.Queries
	cfg     Config
	byCall  map[string][]rule // method -> the blocks it is in
	now     func() time.Time
	rows    atomic.Pointer[map[int64]Restriction]
	kick    func(ctx context.Context, userID int64) error
	blockNs []string
}

// New checks cfg and returns the service with nothing loaded; Register is the usual way in.
func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if cfg.ReloadEvery == 0 {
		cfg.ReloadEvery = 5 * time.Second
	}
	if cfg.ReloadEvery < 0 {
		return nil, fmt.Errorf("restrict: ReloadEvery %s: must be positive", cfg.ReloadEvery)
	}
	s := &Service{q: store.New(pool), cfg: cfg, byCall: map[string][]rule{}, now: time.Now}
	for name, methods := range cfg.Blocks {
		if name == "" || len(methods) == 0 || slices.Contains(methods, "") {
			return nil, fmt.Errorf("restrict: block %q: needs a name and at least one method", name)
		}
		for _, e := range methods {
			m, r, ok := parseEntry(e)
			if !ok {
				return nil, fmt.Errorf("restrict: block %q: entry %q: want method or method?field=value", name, e)
			}
			r.block = name
			s.byCall[m] = append(s.byCall[m], r)
		}
		s.blockNs = append(s.blockNs, name)
	}
	slices.Sort(s.blockNs)
	s.rows.Store(&map[int64]Restriction{})
	return s, nil
}

// BlockNames lists Config.Blocks' names, sorted.
func (s *Service) BlockNames() []string { return slices.Clone(s.blockNs) }

// Load reads every stored restriction into the in-memory copy.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.q.ListRestrictions(ctx)
	if err != nil {
		return err
	}
	m := make(map[int64]Restriction, len(rows))
	for _, row := range rows {
		m[row.UserID] = fromRow(row)
	}
	s.rows.Store(&m)
	return nil
}

func (s *Service) reload(ctx context.Context) {
	if err := s.Load(ctx); err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "restrict: reload", "err", err)
	}
}

// Current is a player's restriction from the in-memory copy, if one is active now.
func (s *Service) Current(userID int64) (Restriction, bool) {
	r, ok := (*s.rows.Load())[userID]
	return r, ok && r.Active(s.now())
}

// Check is nil when the player may call method, judged without its payload: a field entry
// ("method?field=value") counts as matching. Register guards with CheckCall.
func (s *Service) Check(ctx context.Context, userID int64, method string) error {
	return s.check(userID, method, nil, false)
}

// CheckCall is the RPC router's guard (Router.CheckData): nil when the player may make this call.
func (s *Service) CheckCall(_ context.Context, userID int64, method string, data []byte) error {
	return s.check(userID, method, data, true)
}

func (s *Service) check(userID int64, method string, data []byte, withData bool) error {
	r, ok := s.Current(userID)
	switch {
	case !ok:
		return nil
	case r.Suspended:
		return ErrSuspended
	}
	for _, x := range s.byCall[method] {
		if slices.Contains(r.Blocks, x.block) && (!withData || x.matches(data)) {
			return ErrBlocked
		}
	}
	return nil
}

// Login is the login guard: a suspended player gets no token.
func (s *Service) Login(_ context.Context, userID int64) error {
	if r, ok := s.Current(userID); ok && r.Suspended {
		return ErrSuspended
	}
	return nil
}

// Get reads a player's row fresh from Postgres (the zero Restriction when there is none).
func (s *Service) Get(ctx context.Context, userID int64) (Restriction, error) {
	row, err := s.q.GetRestriction(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Restriction{Blocks: []string{}}, nil
	}
	if err != nil {
		return Restriction{}, err
	}
	return fromRow(row), nil
}

// Stored is one player's stored row, for listings.
type Stored struct {
	UserID int64
	Restriction
}

// List pages the stored rows (active or not), newest player first; beforeID 0 is the first page.
func (s *Service) List(ctx context.Context, beforeID int64, limit int32) ([]Stored, error) {
	rows, err := s.q.PageRestrictions(ctx, store.PageRestrictionsParams{BeforeID: beforeID, Lim: limit})
	out := make([]Stored, len(rows))
	for i, row := range rows {
		out[i] = Stored{row.UserID, fromRow(row)}
	}
	return out, err
}

// Set stores a player's restriction in the caller's transaction (an admin console action writes
// its audit row in the same one) and returns the row before and after. The zero Restriction lifts
// everything. Call Applied after the commit.
func (s *Service) Set(ctx context.Context, tx pgx.Tx, userID int64, r Restriction) (before, after Restriction, err error) {
	for _, b := range r.Blocks {
		if _, ok := s.cfg.Blocks[b]; !ok {
			return before, after, fmt.Errorf("%w: %q", ErrUnknownBlock, b)
		}
	}
	q := s.q.WithTx(tx)
	row, err := q.LockRestriction(ctx, userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		before = Restriction{Blocks: []string{}}
	case err != nil:
		return before, after, err
	default:
		before = fromRow(row)
	}
	blocks := slices.Clone(r.Blocks)
	slices.Sort(blocks)
	blocks = slices.Compact(blocks)
	if blocks == nil {
		blocks = []string{}
	}
	until := pgtype.Timestamptz{}
	if r.Until != nil {
		until = pgtype.Timestamptz{Time: *r.Until, Valid: true}
	}
	row, err = q.UpsertRestriction(ctx, store.UpsertRestrictionParams{UserID: userID, Suspended: r.Suspended,
		Blocks: blocks, Note: r.Note, Until: until})
	if err != nil {
		return before, after, err
	}
	return before, fromRow(row), nil
}

// Applied makes a committed Set take effect on this instance at once (others reload within
// ReloadEvery) and drops a newly suspended player's connections.
func (s *Service) Applied(ctx context.Context, userID int64, after Restriction) {
	s.reload(ctx)
	if after.Suspended && after.Active(s.now()) && s.kick != nil {
		if err := s.kick(ctx, userID); err != nil {
			slog.WarnContext(ctx, "restrict: disconnect", "user", userID, "err", err)
		}
	}
}
