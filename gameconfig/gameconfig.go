// Package gameconfig holds what admins can change about games without a deploy: whether a game
// (or one of its variants) takes new bets, and its min bet, max bet and max payout per currency.
// Each game declares its defaults in code. They are hard caps: a stored override can tighten them,
// never loosen them, so a typo can't open a bigger exposure than the code allows. Payout tables and
// house edges are not here; they are pinned to the public verifier.
package gameconfig

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amir1430/gamify/gameconfig/internal/store"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/realtime"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReloadEvery is how soon a change made in the admin reaches the bet path of every instance.
const ReloadEvery = 5 * time.Second

// Channel is where the players' view of the limits (Public) is published after an admin change;
// clients load games.config when they (re)subscribe. In the feed namespace: clients may subscribe.
const Channel = "feed:config"

var (
	ErrInvalid     = &realtime.ClientError{Code: 1100, Message: "invalid bet"}
	ErrUnavailable = &realtime.ClientError{Code: 1107, Message: "this game is temporarily unavailable"}
)

// Limits apply to one bet, in its currency's minor units.
type Limits struct {
	Enabled   bool  `json:"enabled"`
	MinBet    int64 `json:"min_bet"`
	MaxBet    int64 `json:"max_bet"`
	MaxPayout int64 `json:"max_payout"`
}

// Allows reports whether amount is a valid stake: 0 (demo) or MinBet..MaxBet, and below
// MaxPayout (a bet capped at its own stake could never win).
func (l Limits) Allows(amount int64) bool {
	return amount == 0 || amount >= l.MinBet && amount <= l.MaxBet && amount < l.MaxPayout
}

type key struct{ game, currency, variant string }

// snapshot is the stored config, swapped whole on every reload so bets never wait on Postgres.
type snapshot struct {
	settings map[string]store.GameSetting
	limits   map[key]store.GameLimit
}

func newSnapshot(settings []store.GameSetting, limits []store.GameLimit) *snapshot {
	snap := &snapshot{settings: map[string]store.GameSetting{}, limits: map[key]store.GameLimit{}}
	for _, g := range settings {
		snap.settings[g.Game] = g
	}
	for _, l := range limits {
		snap.limits[key{l.Game, l.Currency, l.Variant}] = l
	}
	return snap
}

type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	ledger *ledger.Ledger // the brand's currencies and their minimum stakes
	snap   atomic.Pointer[snapshot]

	mu    sync.Mutex
	games []*Game // registered by the game services, in wiring order

	off map[ledger.Currency]bool // currencies no game takes bets in (Off), set before serving
}

// NewService starts with no overrides (every game on, code defaults) until the first Load.
func NewService(pool *pgxpool.Pool, l *ledger.Ledger) *Service {
	s := &Service{pool: pool, q: store.New(pool), ledger: l}
	s.snap.Store(newSnapshot(nil, nil))
	return s
}

// Off turns off bets in currency c in every game, whatever the stored config says (a currency
// that can't be cashed out, so none can be won from the house). Call before serving.
func (s *Service) Off(c ledger.Currency) {
	if s.off == nil {
		s.off = map[ledger.Currency]bool{}
	}
	s.off[c] = true
}

// OffCurrencies lists the currencies turned off with Off (games.config "off").
func (s *Service) OffCurrencies() []ledger.Currency {
	out := []ledger.Currency{}
	for _, c := range s.ledger.Currencies() {
		if s.off[c] {
			out = append(out, c)
		}
	}
	return out
}

// Load reads the stored config.
func (s *Service) Load(ctx context.Context) error {
	snap, err := s.read(ctx)
	if err == nil {
		s.snap.Store(snap)
	}
	return err
}

func (s *Service) read(ctx context.Context) (*snapshot, error) {
	settings, err := s.q.ListGameSettings(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := s.q.ListGameLimits(ctx)
	if err != nil {
		return nil, err
	}
	return newSnapshot(settings, limits), nil
}

// Run reloads the config every ReloadEvery until ctx ends. On an error the last config stays.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(ReloadEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Load(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "gameconfig reload", "err", err)
			}
		}
	}
}

// Game is one game's handle on its config: its code defaults (the hard caps) and its variants.
type Game struct {
	Name      string
	MaxBet    int64
	MaxPayout int64
	Variants  []string        // keys a game_limits row may use; empty for games without options
	PvP       bool            // takes bets in a currency turned off with Off (PvP pays no winnings out of the house)
	Only      ledger.Currency // "" = every currency; else the only one the game takes
	s         *Service
}

// Game registers a game and returns the handle its service checks bets with.
func (s *Service) Game(name string, maxBet, maxPayout int64, variants []string) *Game {
	g := &Game{Name: name, MaxBet: maxBet, MaxPayout: maxPayout, Variants: variants, s: s}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games = append(s.games, g)
	return g
}

// PvPGame is Game for a player-vs-player game: exempt from Off, since players win each other's
// stakes and the house's USD never pays out.
func (s *Service) PvPGame(name string, maxBet, maxPayout int64, variants []string) *Game {
	g := s.Game(name, maxBet, maxPayout, variants)
	g.PvP = true
	return g
}

// off reports whether bets in c are off for this game (Off, never for PvP; or not its Only currency).
func (g *Game) off(c ledger.Currency) bool {
	return (g.Only != "" && c != g.Only) || (!g.PvP && g.s.off[c])
}

// Games lists the registered games.
func (s *Service) Games() []*Game {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.games)
}

func (s *Service) lookup(name string) *Game {
	for _, g := range s.Games() {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// Limits resolves the limits for a currency ("" = the default) and variant ("" = none): the code defaults,
// then the game's row, then the variant's row (each NULL inherits), clamped to the code defaults
// and the ledger's minimum stake. A game turned off turns off all its variants, a currency turned
// off (Off) every game in it. Returns
// ErrInvalid for an unknown currency and ErrUnavailable when bets are off.
func (g *Game) Limits(c ledger.Currency, variant string) (ledger.Currency, Limits, error) {
	c = cmp.Or(c, g.s.ledger.Default())
	if !g.s.ledger.Valid(c) {
		return c, Limits{}, ErrInvalid
	}
	l := g.resolve(g.s.snap.Load(), c, variant)
	l.Enabled = l.Enabled && !g.off(c)
	if !l.Enabled {
		return c, l, ErrUnavailable
	}
	return c, l, nil
}

func (g *Game) resolve(snap *snapshot, c ledger.Currency, variant string) Limits {
	minBet := g.s.ledger.MinAmount(c)
	l := Limits{Enabled: true, MinBet: minBet, MaxBet: g.MaxBet, MaxPayout: g.MaxPayout}
	if set, ok := snap.settings[g.Name]; ok {
		l.Enabled = set.Enabled
	}
	l.apply(snap.limits, key{g.Name, string(c), ""})
	if variant != "" {
		l.apply(snap.limits, key{g.Name, string(c), variant})
	}
	l.MinBet = max(l.MinBet, minBet)
	l.MaxBet = min(l.MaxBet, g.MaxBet)
	l.MaxPayout = min(l.MaxPayout, g.MaxPayout)
	return l
}

func (l *Limits) apply(rows map[key]store.GameLimit, k key) {
	row, ok := rows[k]
	if !ok {
		return
	}
	l.Enabled = l.Enabled && row.Enabled
	if row.MinBet != nil {
		l.MinBet = *row.MinBet
	}
	if row.MaxBet != nil {
		l.MaxBet = *row.MaxBet
	}
	if row.MaxPayout != nil {
		l.MaxPayout = *row.MaxPayout
	}
}

// Stake is Limits plus the amount check (Allows).
func (g *Game) Stake(c ledger.Currency, variant string, amount int64) (ledger.Currency, Limits, error) {
	c, l, err := g.Limits(c, variant)
	if err == nil && !l.Allows(amount) {
		err = ErrInvalid
	}
	return c, l, err
}

// PublicGame is one game's limits in one currency as players see them (games.config): the whole
// game, and only the variants whose limits differ from it.
type PublicGame struct {
	Limits   Limits            `json:"limits"`
	Variants map[string]Limits `json:"variants,omitempty"`
}

// Public is every registered game's limits per currency, from the cache.
func (s *Service) Public() map[string]map[ledger.Currency]PublicGame {
	snap := s.snap.Load()
	out := map[string]map[ledger.Currency]PublicGame{}
	for _, g := range s.Games() {
		byCur := map[ledger.Currency]PublicGame{}
		for _, c := range s.ledger.Currencies() {
			pg := PublicGame{Limits: g.resolve(snap, c, "")}
			pg.Limits.Enabled = pg.Limits.Enabled && !g.off(c)
			for _, v := range g.Variants {
				l := g.resolve(snap, c, v)
				l.Enabled = l.Enabled && !g.off(c)
				if l != pg.Limits {
					if pg.Variants == nil {
						pg.Variants = map[string]Limits{}
					}
					pg.Variants[v] = l
				}
			}
			byCur[c] = pg
		}
		out[g.Name] = byCur
	}
	return out
}
