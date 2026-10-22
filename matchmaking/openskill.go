// Package matchmaking puts players together, holds their stakes and settles results and ratings.
package matchmaking

import "math"

// Rating is an OpenSkill (Weng-Lin) skill estimate: mu is the mean, sigma the uncertainty.
type Rating struct{ Mu, Sigma float64 }

const (
	rateBeta  = 25.0 / 6
	rateKappa = 1e-4
)

// DefaultRating is the rating of a new player.
func DefaultRating() Rating { return Rating{Mu: 25, Sigma: 25.0 / 3} }

// Ordinal is the conservative skill estimate used for leaderboards and matching.
func (r Rating) Ordinal() float64 { return r.Mu - 3*r.Sigma }

// rate returns the updated ratings after one Plackett-Luce game. ranks are per team, lower is
// better, and equal ranks are a tie. Team shapes are kept.
func rate(teams [][]Rating, ranks []int) [][]Rating {
	n := len(teams)
	mu := make([]float64, n)
	s2 := make([]float64, n)
	var c2 float64
	for i, team := range teams {
		for _, p := range team {
			mu[i] += p.Mu
			s2[i] += p.Sigma * p.Sigma
		}
		c2 += s2[i] + rateBeta*rateBeta
	}
	c := math.Sqrt(c2)

	e := make([]float64, n)
	for i := range teams {
		e[i] = math.Exp(mu[i] / c)
	}
	// sumQ[q] is the strength of every team ranked at or below q; cnt[q] is the size of q's rank.
	sumQ := make([]float64, n)
	cnt := make([]float64, n)
	for q := range teams {
		for i := range teams {
			if ranks[i] >= ranks[q] {
				sumQ[q] += e[i]
			}
		}
		for i := range teams {
			if ranks[i] == ranks[q] {
				cnt[q]++
			}
		}
	}

	out := make([][]Rating, n)
	for i, team := range teams {
		var om, de float64
		for q := range teams {
			if ranks[q] > ranks[i] {
				continue
			}
			p := e[i] / sumQ[q]
			ind := 0.0
			if i == q {
				ind = 1
			}
			om += (ind - p) / cnt[q]
			de += p * (1 - p) / cnt[q]
		}
		omega := s2[i] / c * om
		delta := math.Sqrt(s2[i]) / c * s2[i] / (c * c) * de

		out[i] = make([]Rating, len(team))
		for j, p := range team {
			v := p.Sigma * p.Sigma
			out[i][j] = Rating{
				Mu:    p.Mu + v/s2[i]*omega,
				Sigma: math.Sqrt(v * math.Max(1-v/s2[i]*delta, rateKappa)),
			}
		}
	}
	return out
}
