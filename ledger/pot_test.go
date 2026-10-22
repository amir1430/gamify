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

// Winner i's prize leg is legPotWinner+i, inside the 8-bit leg of a transfer ID.
func TestMaxPotWinners(t *testing.T) {
	if MaxPotWinners != 247 || int(legPotWinner)+MaxPotWinners > 255 {
		t.Fatalf("MaxPotWinners = %d", MaxPotWinners)
	}
}

func TestFeeOf(t *testing.T) {
	for _, c := range []struct{ pot, bp, want int64 }{{1000, 0, 0}, {1000, 250, 25}, {1001, 250, 26}} {
		if got := FeeOf(c.pot, c.bp); got != c.want {
			t.Errorf("FeeOf(%d, %d) = %d, want %d", c.pot, c.bp, got, c.want)
		}
	}
	for p := int64(1); p <= 10_000; p++ {
		if FeeOf(p, FeeBP) != Fee(p) {
			t.Fatalf("FeeOf(%d, FeeBP) != Fee", p)
		}
	}
}
