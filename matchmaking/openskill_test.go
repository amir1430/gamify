package matchmaking

import (
	"math"
	"testing"
)

const rateTol = 1e-9

func approx(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > rateTol {
		t.Errorf("%s = %.15f, want %.15f", what, got, want)
	}
}

func TestRateVectors(t *testing.T) {
	d := DefaultRating()

	t.Run("1v1", func(t *testing.T) {
		got := rate([][]Rating{{d}, {d}}, []int{1, 2})
		approx(t, "winner mu", got[0][0].Mu, 27.63523138347365)
		approx(t, "winner sigma", got[0][0].Sigma, 8.065506316323548)
		approx(t, "loser mu", got[1][0].Mu, 22.36476861652635)
		approx(t, "loser sigma", got[1][0].Sigma, 8.065506316323548)
	})

	t.Run("ffa3", func(t *testing.T) {
		got := rate([][]Rating{{d}, {d}, {d}}, []int{1, 2, 3})
		mu := []float64{27.868876552746237, 25.717219138186557, 21.413904309067206}
		sigma := []float64{8.204837030780652, 8.057829747583876, 8.057829747583876}
		for i := range got {
			approx(t, "ffa mu", got[i][0].Mu, mu[i])
			approx(t, "ffa sigma", got[i][0].Sigma, sigma[i])
		}
	})

	t.Run("draw", func(t *testing.T) {
		got := rate([][]Rating{{d}, {d}}, []int{1, 1})
		for i := range got {
			approx(t, "draw mu", got[i][0].Mu, 25.0)
			approx(t, "draw sigma", got[i][0].Sigma, 8.065506316323548)
		}
	})

	t.Run("2v2", func(t *testing.T) {
		got := rate([][]Rating{
			{d, d},
			{{Mu: 30, Sigma: 5}, {Mu: 20, Sigma: 7}},
		}, []int{2, 1})
		approx(t, "t0p0 mu", got[0][0].Mu, 22.793405924777097)
		approx(t, "t0p0 sigma", got[0][0].Sigma, 8.111584225005174)
		approx(t, "t0p1 mu", got[0][1].Mu, 22.793405924777097)
		approx(t, "t0p1 sigma", got[0][1].Sigma, 8.111584225005174)
		approx(t, "t1p0 mu", got[1][0].Mu, 30.794373867080246)
		approx(t, "t1p0 sigma", got[1][0].Sigma, 4.9653832183847)
		approx(t, "t1p1 mu", got[1][1].Mu, 21.55697277947728)
		approx(t, "t1p1 sigma", got[1][1].Sigma, 6.904691534718088)
	})
}

func TestOrdinalAndDefault(t *testing.T) {
	d := DefaultRating()
	approx(t, "default mu", d.Mu, 25)
	approx(t, "default sigma", d.Sigma, 25.0/3)
	approx(t, "default ordinal", d.Ordinal(), 0)
}
