package ledger

import "testing"

func TestFee(t *testing.T) {
	for pot, want := range map[int64]int64{20: 1, 21: 2, 39: 2, 40: 2, 200: 10, 201: 11, 10_000_000: 500_000} {
		if got := Fee(pot); got != want {
			t.Errorf("Fee(%d) = %d, want %d", pot, got, want)
		}
	}
	for p := int64(2); p <= 10_000; p++ {
		if Fee(p)*10000 < p*500 {
			t.Fatalf("Fee(%d) = %d is below 5%%", p, Fee(p))
		}
	}
}
