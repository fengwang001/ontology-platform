package hll

import (
	"math/rand/v2"
	"testing"
)

// makeHash assembles a 64-bit hash from explicit parts so register behavior is
// verifiable bit by bit:
//   - index occupies the low p bits (register selector);
//   - remaining is the complete 64-p-bit high portion, so the assembled hash
//     is index | remaining<<p and rho = 1 + leading zeros of that field.
//
// remaining must fit in 64-p bits.
func makeHash(tb testing.TB, p int, index, remaining uint64) uint64 {
	tb.Helper()
	width := uint(64 - p)
	if index >= 1<<uint(p) || remaining>>width != 0 {
		panic("makeHash: index or highBits out of range")
	}
	return index | remaining<<uint(p)
}

// distinctHashes deterministically generates n pairwise-distinct pseudo-random
// 64-bit hashes using a splitmix64-style sequence. Distinctness is explicitly
// asserted by callers when required.
func distinctHashes(n int) []uint64 {
	rng := rand.New(rand.NewPCG(0x243F6A8885A308D3, 0x13198A2E03707344))
	out := make([]uint64, n)
	seen := make(map[uint64]struct{}, n)
	for i := range out {
		for {
			h := rng.Uint64()
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				out[i] = h
				break
			}
		}
	}
	return out
}

// shuffledCopy returns a permuted copy of hs.
func shuffledCopy(hs []uint64, seed uint64) []uint64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	out := append([]uint64(nil), hs...)
	rng.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}
