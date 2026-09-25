// Package sel implements quickselect: find the kth smallest element of a
// slice in average O(n) time, rearranging the slice in place.
package sel

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"sync/atomic"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrEmpty = errors.New("sel: empty slice")
	ErrBadK  = errors.New("sel: k out of range")
)

var comparisons atomic.Int64

// Comparisons returns the total number of element comparisons performed
// by all KthSmallest calls so far.
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons resets the comparison counter to zero.
func ResetComparisons() { comparisons.Store(0) }

func less[T cmp.Ordered](a, b T) bool {
	comparisons.Add(1)
	return a < b
}

// KthSmallest returns the element that would sit at index k (0-based) if
// arr were sorted. It may rearrange arr. k must satisfy 0 <= k < len(arr).
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

// partition rearranges arr[lo..hi] around a pivot into
// [<pivot] [pivot] [>pivot] and returns the pivot's final index. The
// pivot is the median of three random samples, which keeps the average
// comparison count well below 4n (a plain random pivot averages ~4n
// when selecting the median).
func partition[T cmp.Ordered](arr []T, lo, hi int) int {
	span := hi - lo + 1
	pi := med3(arr, lo+rand.IntN(span), lo+rand.IntN(span), lo+rand.IntN(span))
	arr[lo], arr[pi] = arr[pi], arr[lo]
	pivot := arr[lo]
	p := lo
	for i := lo + 1; i <= hi; i++ {
		if less(arr[i], pivot) {
			p++
			arr[p], arr[i] = arr[i], arr[p]
		}
	}
	arr[lo], arr[p] = arr[p], arr[lo]
	return p
}

// med3 returns the index (among i, j, k) of the median value.
func med3[T cmp.Ordered](arr []T, i, j, k int) int {
	if less(arr[i], arr[j]) {
		if less(arr[j], arr[k]) {
			return j
		}
		if less(arr[i], arr[k]) {
			return k
		}
		return i
	}
	if less(arr[i], arr[k]) {
		return i
	}
	if less(arr[j], arr[k]) {
		return k
	}
	return j
}
