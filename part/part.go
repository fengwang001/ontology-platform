// Package part provides the partition function and spill decision.
// It depends on no other package in this module.
package part

// Key is a tuple carrying a single non-negative integer key.
type Key int

// H is the partition function h(k) = k mod n (n must be >= 2).
// Build and probe must call this same function with the same n.
func H(k Key, n int) int {
	return int(k) % n
}

// Spilled reports whether a partition holding count build tuples spills
// under the per-partition memory threshold m: a partition spills when it
// keeps more than m build tuples (count > m).
func Spilled(count, m int) bool {
	return count > m
}
