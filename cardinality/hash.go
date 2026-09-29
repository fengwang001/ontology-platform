package cardinality

import (
	"hash/fnv"
	"math/bits"
)

// hashKey maps a key to a deterministic 64-bit hash. FNV-1a has no
// random seed, so the mapping is stable across processes and runs.
func hashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// locate splits a hash into a register index (top precision bits) and
// a rank (leading zeros of the remaining bits plus one). The mapping
// is fixed for a given precision, so a key always lands on the same
// register with the same rank.
func locate(hash uint64, precision uint8) (index uint32, rank uint8) {
	index = uint32(hash >> (64 - precision))
	rest := hash << precision
	if rest == 0 {
		return index, 64 - precision + 1
	}
	return index, uint8(bits.LeadingZeros64(rest)) + 1
}
