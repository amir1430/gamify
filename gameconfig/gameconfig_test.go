package gameconfig

import (
	"errors"
	"testing"

	"github.com/amir1430/gamify/gameconfig/internal/store"
	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/ledgertest"
)

func ptr(v int64) *int64 { return &v }

const usd, tickets ledger.Currency = "usd", "tickets"

func newService() *Service {
	l, err := ledger.New(nil, ledgertest.USD, ledgertest.Tickets)
	if err != nil {
		panic(err)
	}
	return NewService(nil, l)
}

func set(s *Service, settings []store.GameSetting, limits []store.GameLimit) {
	s.snap.Store(newSnapshot(settings, limits))
}

func TestLimits(t *testing.T) {
	s := newService()
	runner := s.Game("runner", 1_000_000, 10_000_000, []string{"low/8", "high/16"})
	arena := s.Game("arena", 1_000_000, 98_000_000, nil)

	// No rows: the code defaults, the ledger minimum, "" = USD.
	c, l, err := runner.Limits("", "high/16")
	if err != nil || c != usd || l != (Limits{true, 1, 1_000_000, 10_000_000}) {
		t.Fatalf("defaults: %v %+v %v", c, l, err)
	}
	if _, l, _ := runner.Limits(tickets, ""); l.MinAmount != 10 {
		t.Fatalf("tickets min: %d", l.MinAmount)
	}
	if _, _, err := runner.Limits("eur", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown currency: %v", err)
	}

	set(s, nil, []store.GameLimit{
		{Game: "runner", Currency: "usd", Enabled: true, MinAmount: ptr(100), MaxAmount: ptr(50_000)},
		{Game: "runner", Currency: "usd", Variant: "high/16", Enabled: true, MaxAmount: ptr(5_000), MaxPayout: ptr(1_000_000)},
		{Game: "runner", Currency: "usd", Variant: "low/8", Enabled: false},
		// Looser than the code: clamped back.
		{Game: "arena", Currency: "tickets", Enabled: true, MinAmount: ptr(1), MaxAmount: ptr(9_000_000), MaxPayout: ptr(1 << 40)},
	})
	for _, tc := range []struct {
		g       *Game
		cur     ledger.Currency
		variant string
		want    Limits
		err     error
	}{
		{runner, usd, "", Limits{true, 100, 50_000, 10_000_000}, nil},                  // game row
		{runner, usd, "high/16", Limits{true, 100, 5_000, 1_000_000}, nil},             // variant inherits min
		{runner, usd, "low/8", Limits{false, 100, 50_000, 10_000_000}, ErrUnavailable}, // variant off
		{runner, usd, "nope", Limits{true, 100, 50_000, 10_000_000}, nil},              // unknown variant: game row
		{runner, tickets, "high/16", Limits{true, 10, 1_000_000, 10_000_000}, nil},     // rows are per currency
		{arena, tickets, "", Limits{true, 10, 1_000_000, 98_000_000}, nil},             // clamped
	} {
		_, got, err := tc.g.Limits(tc.cur, tc.variant)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s %s %q: %+v %v, want %+v %v", tc.g.Name, tc.cur, tc.variant, got, err, tc.want, tc.err)
		}
	}

	// A game turned off turns off every variant, demo play too.
	set(s, []store.GameSetting{{Game: "runner", Enabled: false}}, nil)
	if _, _, err := runner.Check(usd, "high/16", 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("game off: %v", err)
	}
	if _, _, err := arena.Check(usd, "", 100); err != nil {
		t.Fatalf("other game: %v", err)
	}
}

func TestCheck(t *testing.T) {
	s := newService()
	g := s.Game("keno", 1_000_000, 10_000_000, nil)
	set(s, nil, []store.GameLimit{{Game: "keno", Currency: "usd", Enabled: true, MinAmount: ptr(10), MaxAmount: ptr(500)}})
	for amount, ok := range map[int64]bool{0: true, 1: false, 9: false, 10: true, 500: true, 501: false, -1: false} {
		if _, _, err := g.Check(usd, "", amount); (err == nil) != ok {
			t.Errorf("amount %d: %v", amount, err)
		}
	}
	// An amount at or above the payout cap could never win.
	set(s, nil, []store.GameLimit{{Game: "keno", Currency: "usd", Enabled: true, MaxPayout: ptr(400)}})
	for amount, ok := range map[int64]bool{399: true, 400: false, 500: false} {
		if _, _, err := g.Check(usd, "", amount); (err == nil) != ok {
			t.Errorf("amount %d: %v", amount, err)
		}
	}
}

func TestPublic(t *testing.T) {
	s := newService()
	s.Game("runner", 1_000_000, 10_000_000, []string{"low/8", "high/16"})
	set(s, nil, []store.GameLimit{{Game: "runner", Currency: "usd", Variant: "high/16", Enabled: false}})
	p := s.Public()["runner"]
	if p[usd].Limits != (Limits{true, 1, 1_000_000, 10_000_000}) || len(p[usd].Variants) != 1 || p[usd].Variants["high/16"].Enabled {
		t.Fatalf("usd: %+v", p[usd])
	}
	if p[tickets].Variants != nil || p[tickets].Limits.MinAmount != 10 {
		t.Fatalf("tickets: %+v", p[tickets])
	}
}

// A currency turned off accepts no play in any game or variant, demo included; the other is untouched.
func TestOff(t *testing.T) {
	s := newService()
	runner := s.Game("runner", 1_000_000, 10_000_000, []string{"low/8", "high/16"})
	set(s, nil, []store.GameLimit{{Game: "runner", Currency: "usd", Variant: "high/16", Enabled: true, MaxAmount: ptr(500)}})
	s.Off(usd)
	for _, v := range []string{"", "high/16"} {
		if _, _, err := runner.Check("", v, 0); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("usd %q: %v", v, err)
		}
	}
	if _, _, err := runner.Check(tickets, "high/16", 100); err != nil {
		t.Fatalf("tickets: %v", err)
	}
	p := s.Public()["runner"]
	if p[usd].Limits.Enabled || p[usd].Variants["high/16"].Enabled || !p[tickets].Limits.Enabled {
		t.Fatalf("public: %+v", p)
	}
	if off := s.OffCurrencies(); len(off) != 1 || off[0] != usd {
		t.Fatalf("off: %v", off)
	}
}
