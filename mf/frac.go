// Package mf holds the single-allocation primitives for max-min fair share:
// an exact rational Frac, the fair share of a pool, and the per-step decision
// of whether the smallest outstanding demand is fully met. It depends on no
// other package in this module.
package mf

import (
	"math/big"
	"strconv"
)

// Frac is an exact rational N/D with D > 0, kept in lowest terms.
type Frac struct{ N, D int64 }

// Make returns n/d in lowest terms. d must be non-zero; a negative d flips n.
func Make(n, d int64) Frac {
	if d == 0 {
		panic("mf: zero denominator")
	}
	if d < 0 {
		n, d = -n, -d
	}
	g := gcd(abs64(n), d)
	return Frac{N: n / g, D: d / g}
}

// Int returns the integer v as a Frac.
func Int(v int64) Frac { return Frac{N: v, D: 1} }

// Cmp compares exactly by cross multiplication: -1 f<g, 0 f==g, +1 f>g.
func (f Frac) Cmp(g Frac) int {
	l := new(big.Int).Mul(big.NewInt(f.N), big.NewInt(g.D))
	r := new(big.Int).Mul(big.NewInt(g.N), big.NewInt(f.D))
	return l.Cmp(r)
}

// IsZero reports whether the fraction is zero.
func (f Frac) IsZero() bool { return f.N == 0 }

func (f Frac) String() string {
	if f.D == 1 {
		return strconv.FormatInt(f.N, 10)
	}
	return strconv.FormatInt(f.N, 10) + "/" + strconv.FormatInt(f.D, 10)
}

// Fair is the exact fair share of remaining capacity split over tasks tasks.
func Fair(remaining int64, tasks int) Frac {
	if tasks <= 0 {
		panic("mf: fair share over zero tasks")
	}
	return Make(remaining, int64(tasks))
}

// Full reports whether a task with the given demand is fully satisfied at the
// current fair share, i.e. demand <= fair, by exact cross multiplication.
func Full(demand int64, fair Frac) bool {
	l := new(big.Int).Mul(big.NewInt(demand), big.NewInt(fair.D))
	return l.Cmp(big.NewInt(fair.N)) <= 0
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// gcd returns a non-negative greatest common divisor; gcd(0,b)=b, gcd(0,0)=0.
func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
