// Package kad implements Kadane's algorithm for the maximum subarray
// problem over non-empty subarrays.
package kad

import "sync/atomic"

// accesses counts element reads across all calls (observability for tests).
var accesses atomic.Int64

// ElementAccesses reports the total number of element reads so far.
func ElementAccesses() int64 {
	return accesses.Load()
}

// MaxSubarraySum returns the maximum sum over all non-empty subarrays.
// For an empty slice it returns 0; callers needing validation use package arr.
func MaxSubarraySum(arr []int) int {
	_, _, sum := MaxSubarrayRange(arr)
	return sum
}

// MaxSubarrayRange returns lo, hi (inclusive) and the sum of a maximum-sum
// non-empty subarray. Ties resolve to the first maximal interval found.
func MaxSubarrayRange(arr []int) (lo, hi, sum int) {
	if len(arr) == 0 {
		return 0, -1, 0
	}
	accesses.Add(1)
	best, cur, start := arr[0], arr[0], 0
	lo, hi = 0, 0
	for i := 1; i < len(arr); i++ {
		accesses.Add(1)
		// Non-empty semantics: extend the run or restart at a[i],
		// never clamp to 0 (which would allow the empty subarray).
		if cur+arr[i] < arr[i] {
			cur, start = arr[i], i
		} else {
			cur += arr[i]
		}
		if cur > best {
			best, lo, hi = cur, start, i
		}
	}
	return lo, hi, best
}
