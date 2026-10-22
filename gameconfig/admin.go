package gameconfig

import (
	"context"
	"errors"
	"slices"

	"github.com/amir1430/gamify/gameconfig/internal/store"
	"github.com/amir1430/gamify/ledger"

	"github.com/jackc/pgx/v5"
)

var (
	ErrUnknownGame = errors.New("gameconfig: unknown game")
	ErrLoosens     = errors.New("gameconfig: override above the game's code cap")
	// ErrBadOverride covers an unknown currency or variant, a value <= 0, min_amount > max_amount and
	// max_amount >= max_payout (set together), and an empty game name.
	ErrBadOverride = errors.New("gameconfig: invalid override")
)

// LimitOverride is a stored game_limits row; nil = inherit.
type LimitOverride struct {
	Enabled                         bool
	MinAmount, MaxAmount, MaxPayout *int64
}

// Change is what a write replaced and what it stored, for the audit row. A nil side means there
// was no row. For SetGame both are the bool enabled state; for limits, a LimitOverride.
type Change struct{ Before, After any }

func fromRow(l store.GameLimit) LimitOverride {
	return LimitOverride{Enabled: l.Enabled, MinAmount: l.MinAmount, MaxAmount: l.MaxAmount, MaxPayout: l.MaxPayout}
}

func eq(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (o LimitOverride) equal(p LimitOverride) bool {
	return o.Enabled == p.Enabled && eq(o.MinAmount, p.MinAmount) && eq(o.MaxAmount, p.MaxAmount) && eq(o.MaxPayout, p.MaxPayout)
}

// SetGame turns a game on or off (the whole game, all variants), inside tx. It does not reload
// the cache: call Load after tx commits. Change.Before is nil when the game had no row.
func (s *Service) SetGame(ctx context.Context, tx pgx.Tx, game string, enabled bool) (Change, error) {
	if game == "" {
		return Change{}, ErrBadOverride
	}
	if s.lookup(game) == nil {
		return Change{}, ErrUnknownGame
	}
	q := store.New(tx)
	row, err := q.LockGameSetting(ctx, game)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err = q.InsertGameSetting(ctx, store.InsertGameSettingParams{Game: game, Enabled: enabled}); errors.Is(err, pgx.ErrNoRows) {
			// Lost a race with a concurrent insert: lock that row and update it.
			row, err = q.LockGameSetting(ctx, game)
		} else {
			if err != nil {
				return Change{}, err
			}
			return Change{After: enabled}, nil
		}
	}
	if err != nil {
		return Change{}, err
	}
	if row.Enabled != enabled {
		if _, err := q.UpdateGameSetting(ctx, store.UpdateGameSettingParams{Game: game, Enabled: enabled}); err != nil {
			return Change{}, err
		}
	}
	return Change{Before: row.Enabled, After: enabled}, nil
}

// SetLimit stores the override for game, currency and variant ("" = the whole game in that
// currency) inside tx, after checking it can only tighten the game's code caps. It does not
// reload the cache: call Load after tx commits. If the stored row already equals o it is left
// untouched and the Change is still returned.
func (s *Service) SetLimit(ctx context.Context, tx pgx.Tx, game, currency, variant string, o LimitOverride) (Change, error) {
	g, err := s.checkLimitKey(game, currency, variant)
	if err != nil {
		return Change{}, err
	}
	for _, v := range []*int64{o.MinAmount, o.MaxAmount, o.MaxPayout} {
		if v != nil && *v <= 0 {
			return Change{}, ErrBadOverride
		}
	}
	if o.MinAmount != nil && o.MaxAmount != nil && *o.MinAmount > *o.MaxAmount {
		return Change{}, ErrBadOverride
	}
	if o.MaxAmount != nil && o.MaxPayout != nil && *o.MaxAmount >= *o.MaxPayout {
		return Change{}, ErrBadOverride
	}
	if o.MaxAmount != nil && *o.MaxAmount > g.MaxAmount || o.MaxPayout != nil && *o.MaxPayout > g.MaxPayout {
		return Change{}, ErrLoosens
	}
	if !s.effectiveOK(g, currency, variant, o) {
		return Change{}, ErrBadOverride
	}

	q := store.New(tx)
	p := store.InsertGameLimitParams{Game: game, Currency: currency, Variant: variant,
		Enabled: o.Enabled, MinAmount: o.MinAmount, MaxAmount: o.MaxAmount, MaxPayout: o.MaxPayout}
	row, err := q.LockGameLimit(ctx, store.LockGameLimitParams{Game: game, Currency: currency, Variant: variant})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err = q.InsertGameLimit(ctx, p); !errors.Is(err, pgx.ErrNoRows) {
			if err != nil {
				return Change{}, err
			}
			return Change{After: o}, nil
		}
		row, err = q.LockGameLimit(ctx, store.LockGameLimitParams{Game: game, Currency: currency, Variant: variant})
	}
	if err != nil {
		return Change{}, err
	}
	before := fromRow(row)
	if !before.equal(o) {
		if _, err := q.UpdateGameLimit(ctx, store.UpdateGameLimitParams(p)); err != nil {
			return Change{}, err
		}
	}
	return Change{Before: before, After: o}, nil
}

// DeleteLimit removes the override (back to inheriting), inside tx. It does not reload the
// cache: call Load after tx commits. Change.Before is nil, and nothing is deleted, when there was no row.
func (s *Service) DeleteLimit(ctx context.Context, tx pgx.Tx, game, currency, variant string) (Change, error) {
	if _, err := s.checkLimitKey(game, currency, variant); err != nil {
		return Change{}, err
	}
	q := store.New(tx)
	row, err := q.LockGameLimit(ctx, store.LockGameLimitParams{Game: game, Currency: currency, Variant: variant})
	if errors.Is(err, pgx.ErrNoRows) {
		return Change{}, nil
	}
	if err != nil {
		return Change{}, err
	}
	if err := q.DeleteGameLimit(ctx, store.DeleteGameLimitParams{Game: game, Currency: currency, Variant: variant}); err != nil {
		return Change{}, err
	}
	return Change{Before: fromRow(row)}, nil
}

func (s *Service) checkLimitKey(game, currency, variant string) (*Game, error) {
	if game == "" {
		return nil, ErrBadOverride
	}
	g := s.lookup(game)
	if g == nil {
		return nil, ErrUnknownGame
	}
	if !s.ledger.Valid(ledger.Currency(currency)) {
		return nil, ErrBadOverride
	}
	if variant != "" && !slices.Contains(g.Variants, variant) {
		return nil, ErrBadOverride
	}
	return g, nil
}

// Overrides returns the stored config from the cache (no database access): per game whether it is
// enabled, and the limit rows by game, currency and variant.
func (s *Service) Overrides() (settings map[string]bool, limits map[string]map[string]map[string]LimitOverride) {
	snap := s.snap.Load()
	settings = map[string]bool{}
	for g, r := range snap.settings {
		settings[g] = r.Enabled
	}
	limits = map[string]map[string]map[string]LimitOverride{}
	for k, r := range snap.limits {
		if limits[k.game] == nil {
			limits[k.game] = map[string]map[string]LimitOverride{}
		}
		if limits[k.game][k.currency] == nil {
			limits[k.game][k.currency] = map[string]LimitOverride{}
		}
		limits[k.game][k.currency][k.variant] = fromRow(r)
	}
	return settings, limits
}

// effectiveOK reports whether, with o written for (g, currency, variant) over the cached rows,
// every limit set it affects still satisfies MinAmount <= MaxAmount < MaxPayout. A game-level write
// (variant "") also affects every variant, which inherits from it.
func (s *Service) effectiveOK(g *Game, currency, variant string, o LimitOverride) bool {
	snap := s.snap.Load()
	limits := make(map[key]store.GameLimit, len(snap.limits)+1)
	for k, v := range snap.limits {
		limits[k] = v
	}
	limits[key{g.Name, currency, variant}] = store.GameLimit{Game: g.Name, Currency: currency, Variant: variant,
		Enabled: o.Enabled, MinAmount: o.MinAmount, MaxAmount: o.MaxAmount, MaxPayout: o.MaxPayout}
	next := &snapshot{settings: snap.settings, limits: limits}
	vs := []string{variant}
	if variant == "" {
		vs = append(vs, g.Variants...)
	}
	for _, v := range vs {
		l := g.resolve(next, ledger.Currency(currency), v)
		if l.MinAmount > l.MaxAmount || l.MaxAmount >= l.MaxPayout {
			return false
		}
	}
	return true
}
