// Package sel implements quickselect: k-th smallest in average O(n).
package sel

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"sync/atomic"
)

var (
	// ErrBadK is returned when k is out of [0, len(arr)).
	ErrBadK = errors.New("sel: k out of range")
	// ErrEmpty is returned when arr is empty.
	ErrEmpty = errors.New("sel: empty slice")
)

var comparisons atomic.Int64

// Comparisons returns the total element comparisons performed so far.
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons zeroes the comparison counter.
func ResetComparisons() { comparisons.Store(0) }

// KthSmallest returns the element that would sit at index k (0-based)
// if arr were sorted. arr may be reordered. Average O(n) comparisons.
func KthSmallest[T cmp.Ordered](arr []T, k int) (T, error) {
	var zero T
	if len(arr) == 0 {
		return zero, ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		return zero, ErrBadK
	}
	lo, hi := 0, len(arr)-1
	for lo < hi {
		p := partition(arr, lo, hi)
		switch {
		case p == k:
			return arr[p], nil
		case p > k:
			hi = p - 1
		default:
			lo = p + 1
		}
	}
	return arr[lo], nil
}

// partition places a random pivot at its final index p, with smaller
// elements in [lo, p) and greater-or-equal elements in (p, hi].
func partition[T cmp.Ordered](arr []T, lo, hi int) int {
	pi := lo + rand.IntN(hi-lo+1)
	arr[pi], arr[hi] = arr[hi], arr[pi]
	pivot := arr[hi]
	i := lo
	for j := lo; j < hi; j++ {
		comparisons.Add(1)
		if arr[j] < pivot {
			arr[i], arr[j] = arr[j], arr[i]
			i++
		}
	}
	arr[i], arr[hi] = arr[hi], arr[i]
	return i
}
