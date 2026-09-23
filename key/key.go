// Package key defines the totally ordered key type and the deterministic
// tower-level derivation used by the skip list. It depends on nothing.
package key

import "math/bits"

// Key is a string with lexicographic byte order as its total order.
type Key string

// Compare returns -1, 0 or 1 as k is less than, equal to or greater than o.
func (k Key) Compare(o Key) int {
	switch {
	case k < o:
		return -1
	case k > o:
		return 1
	default:
		return 0
	}
}

// Equal reports whether k and o are the same key.
func (k Key) Equal(o Key) bool { return k == o }

// Level deterministically derives a tower height from the key itself:
// 1 + the number of trailing zero bits of the key's FNV-1a hash.
// P(Level >= j) = 2^(1-j), the same distribution coin-flip skip lists
// get randomly, but reproducible: equal keys always get equal levels.
func (k Key) Level() int {
	h := uint64(14695981039346656037)
	for i := 0; i < len(k); i++ {
		h ^= uint64(k[i])
		h *= 1099511628211
	}
	h |= 1 << 63 // cap the height at 64 even for a zero hash
	return 1 + bits.TrailingZeros64(h)
}
