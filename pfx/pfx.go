// Package pfx computes the KMP prefix function and advances the match
// state by a single byte. It has no dependencies on other packages.
package pfx

// Compute returns the prefix function pi for pattern p: pi[i] is the
// length of the longest proper prefix of p[0..i] that is also its
// suffix. pi[0] is 0. p must be non-empty.
func Compute(p []byte) []int {
	pi := make([]int, len(p))
	for i := 1; i < len(p); i++ {
		j := pi[i-1]
		for j > 0 && p[i] != p[j] {
			j = pi[j-1]
		}
		if p[i] == p[j] {
			j++
		}
		pi[i] = j
	}
	return pi
}

// Step advances the KMP state j (length of the consumed-text suffix
// equal to a prefix of p) by one input byte c, following pi fallback
// links, and returns the new state. The caller must keep j < len(p)
// between calls (after a full match, reset j to pi[len(p)-1] first).
// If cmp is non-nil, every character comparison is counted into *cmp;
// the loop structure guarantees at most 2 comparisons amortized per
// byte, since j decreases at most as often as it has increased.
func Step(p []byte, pi []int, j int, c byte, cmp *int) int {
	for {
		if cmp != nil {
			*cmp++
		}
		if p[j] == c {
			return j + 1
		}
		if j == 0 {
			return 0
		}
		j = pi[j-1]
	}
}
