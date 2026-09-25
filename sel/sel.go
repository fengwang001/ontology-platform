// Package sel implements quickselect: find the k-th smallest element
// in average O(n) comparisons without fully sorting the input.
package sel

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"sync/atomic"
)

// Sentinel errors returned by KthSmallest; distinguish with errors.Is.
var (
	ErrEmpty = errors.New("sel: empty slice")
	ErrBadK  = errors.New("sel: k out of range")
)

// compares counts element comparisons across all calls (unexported
// counter, read/reset via Compares/ResetCompares; atomic so concurrent
// calls stay race-free).
var compares atomic.Int64

// Compares returns the number of element comparisons since the last reset.
func Compares() int64 { return compares.Load() }

// ResetCompares zeroes the comparison counter.
func ResetCompares() { compares.Store(0) }

// KthSmallest returns the element that would sit at index k (0-based) if
// arr were sorted. arr may be reordered in place. Average O(n).
func KthSmallest[T cmp.Ordered](arr []T, k int) (T, error) {
	var zero T
	if len(arr) == 0 {
		return zero, ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		return zero, ErrBadK
	}
	lo, hi := 0, len(arr)-1
	for {
		// p is the pivot's final sorted index; k lives in the same
		// index domain, so compare p with k (never pivot value with k).
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
}

// partition rearranges arr[lo..hi] around a random pivot into
// [<pivot] pivot [>=pivot] and returns the pivot's final index.
func partition[T cmp.Ordered](arr []T, lo, hi int) int {
	pi := lo + rand.IntN(hi-lo+1)
	arr[pi], arr[hi] = arr[hi], arr[pi]
	pivot := arr[hi]
	i := lo
	for j := lo; j < hi; j++ {
		compares.Add(1)
		if arr[j] < pivot {
			arr[i], arr[j] = arr[j], arr[i]
			i++
		}
	}
	arr[i], arr[hi] = arr[hi], arr[i]
	return i
}
