package ontology

// splitmix64 is a deterministic 64-bit PRNG used to generate distinct
// test hashes without any external dependency.
type splitmix64 struct{ state uint64 }

func newSplitmix64(seed uint64) *splitmix64 { return &splitmix64{state: seed} }

func (s *splitmix64) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// distinctHashes returns n deterministically generated, pairwise
// distinct hashes.
func distinctHashes(seed uint64, n int) []uint64 {
	gen := newSplitmix64(seed)
	seen := make(map[uint64]struct{}, n)
	out := make([]uint64, 0, n)
	for len(out) < n {
		h := gen.next()
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}
