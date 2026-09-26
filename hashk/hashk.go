// Package hashk provides the two candidate-slot hash functions for cuckoo hashing.
package hashk

// H1 returns the T1 candidate slot: h1(x) = x mod n.
// Callers must guarantee n >= 1.
func H1(x, n int) int {
	r := x % n
	if r < 0 {
		r += n // normalize negative keys into [0, n)
	}
	return r
}

// H2 returns the T2 candidate slot: h2(x) = (3*x + 1) mod n.
// Callers must guarantee n >= 1.
func H2(x, n int) int {
	r := (3*x + 1) % n
	if r < 0 {
		r += n
	}
	return r
}
