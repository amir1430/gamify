package flags

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sync/atomic"
	"time"

	"github.com/amir1430/gamify/flags/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrBadKey        = errors.New("flags: key must match ^[a-z0-9][a-z0-9_.-]{0,63}$")
	ErrExists        = errors.New("flags: a flag with this key exists")
	ErrNotFound      = errors.New("flags: no such flag")
	ErrBadPercent    = errors.New("flags: percent must be 0..100")
	ErrBadType       = errors.New("flags: type must be bool")
	ErrUnknownPlayer = errors.New("flags: unknown player")
)

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// TypeBool is the only flag type for now.
const TypeBool = "bool"

// Config is the module's settings; the zero value is ready to use.
type Config struct {
	// ReloadEvery is how soon a change made on another instance applies here (default 5s).
	ReloadEvery time.Duration
}

// Flag is a stored flag with its testers.
type Flag struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"` // what game code reads; never changes
	Description string    `json:"description"`
	Type        string    `json:"type"` // TypeBool; "" on Create means TypeBool
	Enabled     bool      `json:"enabled"`
	Percent     int       `json:"percent"` // 0..100: the share of players who get it while on
	Testers     []int64   `json:"testers"` // sorted, never nil: they get it while on, whatever Percent
	Version     int64     `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Service reads and writes flags. Reads (Bool, For) use an in-memory copy.
type Service struct {
	pool *pgxpool.Pool
	cfg  Config
	snap atomic.Pointer[map[string]entry]
}

// New checks cfg and returns the service with nothing loaded; Register is the usual way in.
func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if cfg.ReloadEvery < 0 {
		return nil, fmt.Errorf("flags: negative ReloadEvery %v", cfg.ReloadEvery)
	}
	if cfg.ReloadEvery == 0 {
		cfg.ReloadEvery = 5 * time.Second
	}
	s := &Service{pool: pool, cfg: cfg}
	s.snap.Store(&map[string]entry{})
	return s, nil
}

// Load reads every flag and its testers into the in-memory copy.
func (s *Service) Load(ctx context.Context) error {
	q := store.New(s.pool)
	rows, err := q.ListFlags(ctx)
	if err != nil {
		return err
	}
	testers, err := q.ListTesters(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]map[int64]struct{})
	for _, t := range testers {
		if byID[t.FlagID] == nil {
			byID[t.FlagID] = map[int64]struct{}{}
		}
		byID[t.FlagID][t.UserID] = struct{}{}
	}
	m := make(map[string]entry, len(rows))
	for _, r := range rows {
		m[r.Key] = entry{id: r.ID, key: r.Key, enabled: r.Enabled, percent: int(r.Percent), testers: byID[r.ID]}
	}
	s.snap.Store(&m)
	return nil
}

// reload is Load for the loop: a failure is logged and the last copy stays.
func (s *Service) reload(ctx context.Context) {
	if err := s.Load(ctx); err != nil && ctx.Err() == nil {
		slog.ErrorContext(ctx, "flags: reload", "err", err)
	}
}

// Applied makes a committed write take effect on this instance at once (others reload within
// ReloadEvery).
func (s *Service) Applied(ctx context.Context) { s.reload(ctx) }

// Bool is the flag's value for this player; fallback when no flag has the key.
func (s *Service) Bool(userID int64, key string, fallback bool) bool {
	e, ok := (*s.snap.Load())[key]
	if !ok {
		return fallback
	}
	return e.value(userID)
}

// For is every flag's value for this player, by key (what flags.get returns).
func (s *Service) For(userID int64) map[string]any {
	snap := *s.snap.Load()
	out := make(map[string]any, len(snap))
	for k, e := range snap {
		out[k] = e.value(userID)
	}
	return out
}

func fromRow(r store.Flag, testers []int64) Flag {
	if testers == nil {
		testers = []int64{}
	}
	return Flag{ID: r.ID, Key: r.Key, Description: r.Description, Type: r.Type, Enabled: r.Enabled,
		Percent: int(r.Percent), Testers: testers, Version: r.Version, UpdatedAt: r.UpdatedAt.Time}
}

// List reads every flag fresh from Postgres, ordered by key.
func (s *Service) List(ctx context.Context) ([]Flag, error) {
	q := store.New(s.pool)
	rows, err := q.ListFlags(ctx)
	if err != nil {
		return nil, err
	}
	testers, err := q.ListTesters(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64][]int64{}
	for _, t := range testers {
		byID[t.FlagID] = append(byID[t.FlagID], t.UserID)
	}
	out := make([]Flag, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r, byID[r.ID])
	}
	return out, nil
}

// Get reads one flag fresh from Postgres.
func (s *Service) Get(ctx context.Context, id int64) (Flag, error) {
	return get(ctx, store.New(s.pool), id, false)
}

func get(ctx context.Context, q *store.Queries, id int64, lock bool) (Flag, error) {
	var r store.Flag
	var err error
	if lock {
		r, err = q.GetFlagForUpdate(ctx, id)
	} else {
		r, err = q.GetFlag(ctx, id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, ErrNotFound
	}
	if err != nil {
		return Flag{}, err
	}
	testers, err := q.TestersOf(ctx, id)
	if err != nil {
		return Flag{}, err
	}
	return fromRow(r, testers), nil
}

func pgCode(err error) string {
	if pe := (*pgconn.PgError)(nil); errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func setTesters(ctx context.Context, q *store.Queries, id int64, userIDs []int64) error {
	if err := q.DeleteTesters(ctx, id); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return nil
	}
	err := q.InsertTesters(ctx, store.InsertTestersParams{FlagID: id, UserIds: userIDs})
	if pgCode(err) == "23503" /* foreign_key_violation: users */ {
		return ErrUnknownPlayer
	}
	return err
}

// Create stores a new flag, with f.Testers if any, in the console's tx. f.ID and f.Version are
// ignored.
func (s *Service) Create(ctx context.Context, tx pgx.Tx, f Flag) (Flag, error) {
	if !keyRe.MatchString(f.Key) {
		return Flag{}, ErrBadKey
	}
	if f.Type == "" {
		f.Type = TypeBool
	}
	if f.Type != TypeBool {
		return Flag{}, ErrBadType
	}
	if f.Percent < 0 || f.Percent > 100 {
		return Flag{}, ErrBadPercent
	}
	q := store.New(tx)
	r, err := q.InsertFlag(ctx, store.InsertFlagParams{Key: f.Key, Description: f.Description, Type: f.Type,
		Enabled: f.Enabled, Percent: int16(f.Percent)})
	if pgCode(err) == "23505" /* unique_violation: key */ {
		return Flag{}, ErrExists
	}
	if err != nil {
		return Flag{}, err
	}
	if err := setTesters(ctx, q, r.ID, f.Testers); err != nil {
		return Flag{}, err
	}
	return get(ctx, q, r.ID, false)
}

// Update changes f.ID's description, switch and percent in the console's tx; the rest of f is
// ignored.
func (s *Service) Update(ctx context.Context, tx pgx.Tx, f Flag) (before, after Flag, err error) {
	if f.Percent < 0 || f.Percent > 100 {
		return before, after, ErrBadPercent
	}
	q := store.New(tx)
	if before, err = get(ctx, q, f.ID, true); err != nil {
		return before, after, err
	}
	if _, err = q.UpdateFlag(ctx, store.UpdateFlagParams{ID: f.ID, Description: f.Description,
		Enabled: f.Enabled, Percent: int16(f.Percent)}); err != nil {
		return before, after, err
	}
	after, err = get(ctx, q, f.ID, false)
	return before, after, err
}

// SetTesters replaces flag id's testers in the console's tx (duplicates are stored once).
func (s *Service) SetTesters(ctx context.Context, tx pgx.Tx, id int64, userIDs []int64) (before, after Flag, err error) {
	q := store.New(tx)
	if before, err = get(ctx, q, id, true); err != nil {
		return before, after, err
	}
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err = setTesters(ctx, q, id, ids); err != nil {
		return before, after, err
	}
	if _, err = q.TouchFlag(ctx, id); err != nil {
		return before, after, err
	}
	after, err = get(ctx, q, id, false)
	return before, after, err
}

// Delete removes flag id and its testers in the console's tx and returns it as it was. Game code
// reading its key gets its fallback.
func (s *Service) Delete(ctx context.Context, tx pgx.Tx, id int64) (before Flag, err error) {
	q := store.New(tx)
	if before, err = get(ctx, q, id, true); err != nil {
		return before, err
	}
	_, err = q.DeleteFlag(ctx, id)
	return before, err
}
