// Package rng provides an injectable, deterministic pseudo-random source.
package rng

// Source is a uniform integer random source that can be seeded explicitly.
type Source interface {
	// Intn returns a pseudo-random integer in [0, n).
	Intn(n int) int
}

const (
	mul = 6364136223846793005
	inc = 1442695040888963407
)

// pcg is a small deterministic 64-bit LCG-based source. It holds no shared
// state, so sources built with distinct seeds never interfere.
type pcg struct {
	state uint64
}

// New returns a Source whose sequence depends only on seed.
func New(seed uint64) Source {
	return &pcg{state: seed*mul + inc}
}

// Intn returns a value uniformly in [0, n). n must be positive.
func (p *pcg) Intn(n int) int {
	if n <= 1 {
		return 0
	}
	p.state = p.state*mul + inc
	x := p.state
	x ^= x >> 30
	x *= 1442431967
	return int(x % uint64(n))
}
