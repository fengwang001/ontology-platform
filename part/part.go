// Package part implements in-place three-way (Dutch national flag)
// partitioning of a slice around a pivot.
package part

import (
	"cmp"
	"sync/atomic"
)

// swaps counts swap operations performed by ThreeWayPartition.
// It exists to prove the O(n) swap bound and is safe for concurrent use.
var swaps atomic.Int64

// Swaps reports the total number of swaps performed so far.
func Swaps() int64 { return swaps.Load() }

// ResetSwaps resets the swap counter to zero.
func ResetSwaps() { swaps.Store(0) }

// ThreeWayPartition rearranges arr in place into three consecutive
// segments [< pivot | == pivot | > pivot] and returns the start (lt)
// and end-exclusive (gt) bounds of the equal-to-pivot segment.
//
// Invariants: [0,lt) < pivot, [lt,i) == pivot, [gt,len) > pivot,
// [i,gt) not yet scanned. Runs in O(n) time and O(1) extra space.
func ThreeWayPartition[T cmp.Ordered](arr []T, pivot T) (lt, gt int) {
	lt, gt = 0, len(arr)
	i := 0
	for i < gt {
		switch {
		case arr[i] < pivot:
			arr[lt], arr[i] = arr[i], arr[lt]
			swaps.Add(1)
			lt++
			i++
		case arr[i] > pivot:
			gt--
			arr[i], arr[gt] = arr[gt], arr[i]
			swaps.Add(1)
			// i is not advanced: the swapped-in element is unscanned.
		default:
			i++
		}
	}
	return lt, gt
}
