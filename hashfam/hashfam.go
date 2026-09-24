// Package hashfam derives a deterministic family of hash functions from
// parameters alone: no random source, no time, no map iteration order.
package hashfam

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
	mixConst  = 0x9E3779B97F4A7C15
	mixA      = 0xBF58476D1CE4E5B9
	mixB      = 0x94D049BB133111EB
)

// Family is a set of depth pairwise-distinct hash functions.
type Family struct {
	depth int
}

// New derives a family of the given depth. Same depth, same hashes, always.
func New(depth int) Family {
	return Family{depth: depth}
}

// Depth returns the number of hash functions in the family.
func (f Family) Depth() int {
	return f.depth
}

// salt deterministically derives the per-row constant (splitmix64 of the row).
func salt(row int) uint64 {
	z := uint64(row+1) * mixConst
	z = (z ^ (z >> 30)) * mixA
	z = (z ^ (z >> 27)) * mixB
	return z ^ (z >> 31)
}

// Hash maps key into [0, w) for the given row. Pure function of (row, key, w).
func (f Family) Hash(row int, key string, w int) int {
	h := fnvOffset ^ salt(row)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= fnvPrime
	}
	return int(h % uint64(w))
}
