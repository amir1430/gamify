// Package payments moves real money in and out of the ledger through brand-supplied drivers: a
// Receiver per deposit method (crypto, cards) and a Sender per withdrawal method.
package payments

import (
	"cmp"
	"context"
	"embed"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/payments/internal/store"
	"github.com/amir1430/gamify/realtime"
)

// Migrations is the module's tables (goose table goose_payments).
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Game is payments' ID space in the ledger (a platform game, below ledger.FirstBrandGame): its
// deposit and withdrawal transfers are keyed by row ID within it. Never change it.
const Game ledger.Game = 2

var ErrUnknownMethod = &realtime.ClientError{Code: 1040, Message: "unknown payment method"}

// Receiver takes deposits for one method. Run listens for incoming payments (a webhook on h.Mux, a
// chain watcher) and reports each through h.Received; it is restarted with a backoff if it returns.
type Receiver interface {
	Name() string
	Deposit(ctx context.Context, req DepositRequest) (DepositInfo, error)
	Run(ctx context.Context, h *Host) error
}

// Sender pays out for one method. Send must be idempotent per Payout.ID. Send and Status must
// honor ctx: it ends shortly before the withdrawal's lease, after which another worker may ask
// again (and re-send on StateUnknown) and an operator may Resolve it as failed.
type Sender interface {
	Name() string
	ValidateDestination(dest string) error
	Send(ctx context.Context, p Payout) (Result, error)
	Status(ctx context.Context, payoutID int64) (Result, error)
}

type DepositRequest struct {
	UserID   int64
	Currency ledger.Currency
	Amount   int64 // 0: an open address
}

// DepositInfo is how to pay: an Address, or an invoice (InvoiceID, URL, Amount, ExpiresAt).
type DepositInfo struct {
	Address   string    `json:"address"`
	InvoiceID string    `json:"invoice_id"`
	URL       string    `json:"url,omitempty"`
	Amount    int64     `json:"amount"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

type Payout struct {
	ID, UserID  int64
	Currency    ledger.Currency
	Amount      int64 // after the fee
	Destination string
}

type State string

const (
	StatePending State = "pending"
	StatePaid    State = "paid"
	StateFailed  State = "failed"
	StateUnknown State = "unknown" // Status only: the sender never saw this ID
)

type Result struct {
	State       State
	Ref, Reason string
}

// Incoming is one payment a Receiver saw; ExternalID is its own unique ID for it.
type Incoming struct {
	ExternalID, Address, InvoiceID string
	Currency                       ledger.Currency
	Amount                         int64
}

// Host is what a Receiver's Run gets: the HTTP mux for webhooks and a way to report payments.
// Only payments constructs it (one per method); a zero Host's Received returns an error.
type Host struct {
	Mux    *http.ServeMux
	Method string
	s      *Service
}

// DepositLimits: zero is no limit. Off stops new deposit requests (money already on its way is
// still credited).
type DepositLimits struct {
	Min int64
	Off bool
}

// WithdrawLimits: zero means no limit / none, except AutoApproveUpTo, where 0 is always manual.
// Off stops new withdrawal requests.
type WithdrawLimits struct {
	Min, Max, DailyCap, Fee, AutoApproveUpTo, SecondApprovalAbove int64
	Off                                                           bool
}

// Config: zero fields take the defaults (Tick 2 s, Lease 1 min). Tick is how often due withdrawals
// are sent or asked about. Lease is how long one worker owns a withdrawal's Send or Status call,
// and so how often a pending payout is asked about again; the call's ctx ends min(Lease/10, 5 s)
// before the lease does.
type Config struct{ Tick, Lease time.Duration }

type key struct {
	c      ledger.Currency
	method string
}

type Service struct {
	a   *app.App
	q   *store.Queries
	cfg Config

	mu        sync.Mutex
	receivers map[key]Receiver
	running   map[string]Receiver // method name -> the one Receiver whose Run was started
	senders   map[key]Sender
	deposit   map[key]DepositLimits
	withdraw  map[key]WithdrawLimits

	sending sync.WaitGroup // the RPC's background sends
}

func Register(ctx context.Context, a *app.App, cfg Config) (*Service, error) {
	if cfg.Tick < 0 || cfg.Lease < 0 {
		return nil, fmt.Errorf("payments: negative Tick %v or Lease %v", cfg.Tick, cfg.Lease)
	}
	if err := a.Ledger.RegisterGame(Game, "payments"); err != nil {
		return nil, err
	}
	if err := a.Migrate(ctx, Migrations, "goose_payments"); err != nil {
		return nil, err
	}
	if cfg.Tick == 0 {
		cfg.Tick = 2 * time.Second
	}
	if cfg.Lease == 0 {
		cfg.Lease = time.Minute
	}
	s := &Service{a: a, q: store.New(a.Pool), cfg: cfg,
		receivers: map[key]Receiver{}, running: map[string]Receiver{}, senders: map[key]Sender{},
		deposit: map[key]DepositLimits{}, withdraw: map[key]WithdrawLimits{}}
	a.Router.Handle("payments.deposit", realtime.JSON(s.depositRPC))
	a.Router.Handle("payments.withdraw", realtime.JSON(s.withdrawRPC))
	a.Router.Handle("payments.cancel", realtime.JSON(s.cancelRPC))
	a.Router.Handle("payments.history", realtime.JSON(s.historyRPC))
	a.History("payments", s.history)
	a.Reconcile(s.Reconcile)
	a.Loop(cfg.Tick, s.sendDue)
	return s, nil
}

func checkName(name string) error {
	if name == "" || strings.ContainsAny(name, "/ ") {
		return fmt.Errorf("payments: method name %q: must be non-empty, without / or space", name)
	}
	return nil
}

// Receiver registers r as a deposit method for c (call before app.Run). Its Run is started once
// per method name: the same Receiver may serve several currencies, but a different one under a
// name already taken is refused.
func (s *Service) Receiver(c ledger.Currency, r Receiver, l DepositLimits) error {
	name := r.Name()
	if err := checkName(name); err != nil {
		return err
	}
	if !s.a.Ledger.Valid(c) {
		return fmt.Errorf("payments: receiver %q: unknown currency %q", name, c)
	}
	if err := l.validate(); err != nil {
		return fmt.Errorf("payments: receiver %q: %w", name, err)
	}
	k := key{c, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receivers[k] != nil {
		return fmt.Errorf("payments: receiver %q registered twice for %s", name, c)
	}
	if other, ok := s.running[name]; ok && other != r {
		return fmt.Errorf("payments: receiver %q: another Receiver already has this name", name)
	}
	s.receivers[k] = r
	s.deposit[k] = l
	if s.running[name] == nil {
		s.running[name] = r
		h := &Host{Mux: s.a.HTTP, Method: name, s: s}
		s.a.Go("payments.receiver."+name, func(ctx context.Context) error { return r.Run(ctx, h) })
	}
	return nil
}

// Sender registers snd as a withdrawal method for c, which must be a Withdrawable currency.
func (s *Service) Sender(c ledger.Currency, snd Sender, l WithdrawLimits) error {
	name := snd.Name()
	if err := checkName(name); err != nil {
		return err
	}
	if !s.a.Ledger.Withdrawable(c) {
		return fmt.Errorf("payments: sender %q: currency %q is not withdrawable", name, c)
	}
	if err := l.validate(); err != nil {
		return fmt.Errorf("payments: sender %q: %w", name, err)
	}
	k := key{c, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.senders[k] != nil {
		return fmt.Errorf("payments: sender %q registered twice for %s", name, c)
	}
	s.senders[k] = snd
	s.withdraw[k] = l
	return nil
}

// MethodInfo is one registered method of one kind ("deposit" or "withdraw").
type MethodInfo struct {
	Currency ledger.Currency
	Name     string
	Kind     string
}

// Methods lists every registered Receiver and Sender, ordered by currency, name, then kind.
func (s *Service) Methods() []MethodInfo {
	s.mu.Lock()
	var out []MethodInfo
	for k := range s.receivers {
		out = append(out, MethodInfo{k.c, k.method, "deposit"})
	}
	for k := range s.senders {
		out = append(out, MethodInfo{k.c, k.method, "withdraw"})
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b MethodInfo) int {
		return cmp.Or(cmp.Compare(a.Currency, b.Currency), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Kind, b.Kind))
	})
	return out
}
