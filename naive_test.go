package ontology

import "math/rand/v2"

// naive is an independent, deliberately dumb per-bit implementation used
// as the differential-testing oracle for the compressed operations.
type naive map[uint32]bool

func (n naive) set(bit uint32)   { n[bit] = true }
func (n naive) clear(bit uint32) { delete(n, bit) }

func (n naive) setRange(lo, hi uint32) {
	for b := lo; ; b++ {
		n.set(b)
		if b == hi {
			return
		}
	}
}

func (n naive) union(o naive) naive {
	out := naive{}
	for b := range n {
		out.set(b)
	}
	for b := range o {
		out.set(b)
	}
	return out
}

func (n naive) intersect(o naive) naive {
	out := naive{}
	for b := range n {
		if o[b] {
			out.set(b)
		}
	}
	return out
}

func (n naive) difference(o naive) naive {
	out := naive{}
	for b := range n {
		if !o[b] {
			out.set(b)
		}
	}
	return out
}

// sameBits reports whether s and n agree on every bit in [0, limit).
func sameBits(s *Set, n naive, limit uint32) bool {
	for b := uint32(0); b < limit; b++ {
		if s.Contains(b) != n[b] {
			return false
		}
	}
	return true
}

// randomSet builds a set of random runs within [0, limit).
func randomSet(rng *rand.Rand, limit uint32, maxRuns int) (*Set, naive) {
	s := New()
	n := naive{}
	for range rng.IntN(maxRuns) + 1 {
		lo := rng.Uint32N(limit)
		hi := lo + rng.Uint32N(limit/8+1)
		if hi >= limit {
			hi = limit - 1
		}
		for b := lo; b <= hi; b++ {
			s.Set(b)
			n.set(b)
		}
	}
	return s, n
}
