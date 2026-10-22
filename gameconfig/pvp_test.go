package gameconfig

import (
	"testing"
)

func TestPvPIgnoresOff(t *testing.T) {
	s := newService()
	s.Off(usd)
	house := s.Game("arena", 100, 1000, nil)
	pvp := s.PvPGame("flipduel", 100, 1000, nil)
	if _, _, err := house.Limits(usd, ""); err != ErrUnavailable {
		t.Fatalf("house game in an off currency: %v", err)
	}
	if _, l, err := pvp.Limits(usd, ""); err != nil || !l.Enabled {
		t.Fatalf("pvp game in an off currency: %v %+v", err, l)
	}
	pub := s.Public()
	if !pub["flipduel"][usd].Limits.Enabled || pub["arena"][usd].Limits.Enabled {
		t.Fatalf("public: %+v", pub)
	}
}

func TestOnlyCurrency(t *testing.T) {
	s := newService()
	g := s.PvPGame("predict", 100, 1000, nil)
	g.Only = usd
	if _, _, err := g.Limits(tickets, ""); err != ErrUnavailable {
		t.Fatalf("tickets: %v", err)
	}
	if _, _, err := g.Limits(usd, ""); err != nil {
		t.Fatalf("usd: %v", err)
	}
	s.Off(usd)
	if _, _, err := g.Limits(usd, ""); err != nil {
		t.Fatalf("usd after Off: %v", err)
	}
}
