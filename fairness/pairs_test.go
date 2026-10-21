package fairness

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPairsOfHidesUnrevealed(t *testing.T) {
	s, userID := service(t)
	ctx := context.Background()
	if _, err := s.Info(ctx, userID); err != nil {
		t.Fatal(err)
	}
	check := func(pairs []Pair) {
		t.Helper()
		for _, p := range pairs {
			if p.Status == "revealed" {
				continue
			}
			if p.ServerSeed != "" {
				t.Fatalf("%s pair %d exposes its server seed", p.Status, p.ID)
			}
			b, _ := json.Marshal(p)
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			if v, ok := m["server_seed"]; ok {
				t.Fatalf("%s pair JSON has server_seed %v", p.Status, v)
			}
		}
	}
	pairs, err := s.PairsOf(ctx, userID, 0, 10)
	if err != nil || len(pairs) != 2 {
		t.Fatalf("pairs = %+v, %v; want active and next", pairs, err)
	}
	check(pairs)

	rev, err := s.Rotate(ctx, userID, "mine")
	if err != nil {
		t.Fatal(err)
	}
	pairs, err = s.PairsOf(ctx, userID, 0, 10)
	if err != nil || len(pairs) != 3 {
		t.Fatalf("pairs = %+v, %v; want 3", pairs, err)
	}
	check(pairs)
	for i := 1; i < len(pairs); i++ {
		if pairs[i].ID >= pairs[i-1].ID {
			t.Fatalf("not newest first: %+v", pairs)
		}
	}
	var got *Pair
	for i, p := range pairs {
		if p.Status == "revealed" {
			got = &pairs[i]
		}
	}
	if got == nil || got.ServerSeed != rev.ServerSeed || got.ServerSeedHash != rev.ServerSeedHash ||
		got.ClientSeed != rev.ClientSeed || got.Nonce != rev.Nonces || got.RevealedAt == nil || got.CreatedAt.IsZero() {
		t.Fatalf("revealed pair = %+v, want %+v", got, rev)
	}
	older, err := s.PairsOf(ctx, userID, pairs[0].ID, 1)
	if err != nil || len(older) != 1 || older[0].ID != pairs[1].ID {
		t.Fatalf("page 2 = %+v, %v", older, err)
	}
}
