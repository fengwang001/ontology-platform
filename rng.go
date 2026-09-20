// Package ontology provides a reproducible weighted reservoir sampler.
package ontology

// splitmix64 is a small, deterministic 64-bit PRNG.
// It is not cryptographic; it exists so that sampling is fully
// reproducible from an explicit seed without touching global state.
type splitmix64 struct {
	state uint64
}

func newRNG(seed uint64) *splitmix64 {
	return &splitmix64{state: seed}
}

// next returns the next uint64 in the sequence.
func (r *splitmix64) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// uniform returns a float64 in the interval (0, 1].
// The open lower bound guarantees math.Pow(u, 1/w) stays finite.
func (r *splitmix64) uniform() float64 {
	// 53 random bits, shifted into [1, 2^53], then scaled into (0, 1].
	return float64((r.next()>>11)+1) * (1.0 / (1 << 53))
}
