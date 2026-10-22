package matchmaking

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/gameconfig"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/matchmaking/internal/store"
	"github.com/amir1430/gamify/party"
	"github.com/amir1430/gamify/realtime"
)

// Game is matchmaking's ledger game ID (platform game 1); never changed.
const Game ledger.Game = 1

// Forever as MaxWait: a ticket never expires.
const Forever time.Duration = -1

// Migrations is the module's tables (goose table goose_matchmaking).
//
//go:embed migrations/*.sql
var Migrations embed.FS

var (
	ErrQueued         = &realtime.ClientError{Code: 1030, Message: "already queued"}
	ErrBusy           = &realtime.ClientError{Code: 1031, Message: "already in a match"}
	ErrUnknownQueue   = &realtime.ClientError{Code: 1032, Message: "unknown queue"}
	ErrPartyTooBig    = &realtime.ClientError{Code: 1033, Message: "party does not fit the queue"}
	ErrNotFound       = &realtime.ClientError{Code: 1034, Message: "match not found"}
	ErrResultConflict = &realtime.ClientError{Code: 1035, Message: "conflicting result"}
	ErrClosed         = &realtime.ClientError{Code: 1036, Message: "queue closed"}
)

// Team is the size range of one team.
type Team struct{ Min, Max int }

// NumberRule lets two tickets' numbers differ by Diff, plus Widen per Every of the older wait.
type NumberRule struct {
	Diff, Widen int64
	Every       time.Duration
}

// QueueConfig describes one queue. Zero fields take the defaults noted.
type QueueConfig struct {
	Name  string
	Game  *gameconfig.Game // limits for stakes; required when Stakes is set
	Teams []Team
	FFA   *Team // non-nil: Teams must be [{1,1}]; FFA.Min..Max one-player teams

	Ranked       bool
	RatingKey    string  // default Name
	RatingWindow float64 // ordinal difference allowed: Window + Widen per 10 s of the oldest wait; default 5
	RatingWiden  float64 // default 5

	Stakes map[ledger.Currency][]int64 // allowed stakes per currency; nil: no money
	FeeBP  int64                       // 0: ledger.FeeBP; set NoFee for a real 0
	NoFee  bool
	// DrawRefund refunds stakes on a drawn result instead of splitting the pot.
	DrawRefund bool

	Properties []string // ticket properties that must be equal
	Numbers    map[string]NumberRule
	Compatible func(a, b Ticket) bool
	// Prepare puts server-side properties (a league's tier) into t.Server; equal Server maps are
	// required to match.
	Prepare func(ctx context.Context, t *Ticket) error

	FillAfter, MaxWait, MatchTimeout time.Duration // defaults 10 s, 2 min, 1 h
	AllowOffline, Concurrent         bool

	OnMatch, OnFinish func(ctx context.Context, m Match) error
	Payout            func(pot int64, m Match, r Result) []ledger.PotWin
}

// Queue is a validated queue. Config returns its QueueConfig with the defaults filled.
type Queue struct{ cfg QueueConfig }

// Config returns the validated configuration, defaults filled.
func (q *Queue) Config() QueueConfig { return q.cfg }

type Ticket struct {
	ID         int64             `json:"id"`
	Queue      string            `json:"queue"`
	Leader     int64             `json:"leader"`
	PartyID    int64             `json:"party_id"`
	Users      []int64           `json:"users"`
	Currency   ledger.Currency   `json:"currency"`
	Stake      int64             `json:"stake"`
	Properties map[string]string `json:"properties"`
	Server     map[string]string `json:"server"`
	Numbers    map[string]int64  `json:"numbers"`
	Rating     float64           `json:"rating"` // mean ordinal of Users under RatingKey
	QueuedAt   time.Time         `json:"queued_at"`
}

type Player struct {
	EntryID int64 `json:"entry_id"`
	UserID  int64 `json:"user_id"`
	PartyID int64 `json:"party_id"`
	Team    int   `json:"team"`
}

// Result is the placement of each team, 1 = best, ties share a rank.
type Result struct {
	Ranks []int `json:"ranks"`
}

type Match struct {
	ID        int64           `json:"id"`
	Queue     string          `json:"queue"`
	Ref       string          `json:"ref"`
	Status    string          `json:"status"` // forming, running, finishing, settled, cancelling, cancelled
	Currency  ledger.Currency `json:"currency"`
	Stake     int64           `json:"stake"`
	FeeBP     int64           `json:"fee_bp"`
	Players   []Player        `json:"players"`
	Result    *Result         `json:"result,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Deadline  time.Time       `json:"deadline"`
}

// Config's zero fields take the defaults.
type Config struct {
	Tick time.Duration // default 1 s
}

type Service struct {
	a       *app.App
	parties *party.Service
	q       *store.Queries
	cfg     Config

	mu     sync.Mutex
	queues map[string]*Queue
}

// Register adds matchmaking to an app: its tables, the matcher loop and reconcile pass.
func Register(ctx context.Context, a *app.App, parties *party.Service, cfg Config) (*Service, error) {
	if err := a.Migrate(ctx, Migrations, "goose_matchmaking"); err != nil {
		return nil, err
	}
	if cfg.Tick <= 0 {
		cfg.Tick = time.Second
	}
	if err := a.Ledger.RegisterGame(Game, "matchmaking"); err != nil {
		return nil, err
	}
	s := &Service{a: a, parties: parties, q: store.New(a.Pool), cfg: cfg}
	a.Loop(cfg.Tick, s.tick)
	a.Reconcile(s.Reconcile)
	s.handle()
	parties.OnChange(s.onPartyChange)
	return s, nil
}

func (s *Service) tick(ctx context.Context) {} // Task 8 fills it

// Reconcile finishes matches a crash left half-done.
func (s *Service) Reconcile(ctx context.Context) {} // Task 8 fills it

// Queue validates c, fills its defaults and registers it; a duplicate Name is an error.
func (s *Service) Queue(c QueueConfig) (*Queue, error) {
	c, err := c.validate()
	if err != nil {
		return nil, fmt.Errorf("matchmaking: queue %q: %w", c.Name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.queues[c.Name]; ok {
		return nil, fmt.Errorf("matchmaking: queue %q registered twice", c.Name)
	}
	if s.queues == nil {
		s.queues = map[string]*Queue{}
	}
	q := &Queue{cfg: c}
	s.queues[c.Name] = q
	return q, nil
}

func (c QueueConfig) validate() (QueueConfig, error) {
	if c.Name == "" {
		return c, errors.New("empty name")
	}
	if len(c.Teams) == 0 {
		return c, errors.New("no teams")
	}
	for _, t := range c.Teams {
		if t.Min < 1 || t.Min > t.Max {
			return c, fmt.Errorf("team size %d..%d", t.Min, t.Max)
		}
	}
	if c.FFA != nil && (len(c.Teams) != 1 || c.Teams[0] != Team{1, 1}) {
		return c, errors.New("FFA needs Teams [{1,1}]")
	}
	if c.FFA == nil && len(c.Teams) < 2 {
		return c, errors.New("need at least 2 teams (or FFA)")
	}
	if c.FFA != nil && (c.FFA.Min < 2 || c.FFA.Min > c.FFA.Max) {
		return c, fmt.Errorf("FFA size %d..%d", c.FFA.Min, c.FFA.Max)
	}
	if c.Stakes != nil && c.Game == nil {
		return c, errors.New("stakes need a Game")
	}
	for cur, ss := range c.Stakes {
		if len(ss) == 0 {
			return c, fmt.Errorf("no stakes in %s", cur)
		}
		for _, v := range ss {
			if v <= 0 {
				return c, fmt.Errorf("stake %d in %s", v, cur)
			}
		}
	}
	for k, r := range c.Numbers {
		if r.Every <= 0 {
			return c, fmt.Errorf("number %q: Every %v", k, r.Every)
		}
	}
	if c.FillAfter < 0 || c.MatchTimeout < 0 || c.MaxWait < 0 && c.MaxWait != Forever {
		return c, errors.New("negative FillAfter, MatchTimeout or MaxWait")
	}
	if c.FeeBP < 0 || c.FeeBP > 10_000 {
		return c, fmt.Errorf("FeeBP %d outside 0..10000", c.FeeBP)
	}
	if c.FeeBP == 0 && !c.NoFee {
		c.FeeBP = ledger.FeeBP
	}
	if c.RatingKey == "" {
		c.RatingKey = c.Name
	}
	if c.RatingWindow == 0 {
		c.RatingWindow = 5
	}
	if c.RatingWiden == 0 {
		c.RatingWiden = 5
	}
	if c.FillAfter == 0 {
		c.FillAfter = 10 * time.Second
	}
	if c.MaxWait == 0 {
		c.MaxWait = 2 * time.Minute
	}
	if c.MatchTimeout == 0 {
		c.MatchTimeout = time.Hour
	}
	return c, nil
}
