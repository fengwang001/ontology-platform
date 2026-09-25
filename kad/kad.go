// Package kad implements Kadane's algorithm for the maximum subarray sum.
// Subarrays are non-empty: on all-negative input the result is the
// largest (least negative) element, not the empty subarray sum 0.
package kad

import "sync/atomic"

// accesses counts element reads across all calls, for complexity audits.
var accesses atomic.Int64

// AccessCount reports the total number of element accesses so far.
func AccessCount() int64 { return accesses.Load() }

// ResetAccessCount resets the element access counter to zero.
func ResetAccessCount() { accesses.Store(0) }

// MaxSubarraySum returns the maximum sum of any non-empty contiguous
// subarray of arr. It returns 0 for an empty slice.
func MaxSubarraySum(arr []int) int {
	if len(arr) == 0 {
		return 0
	}
	accesses.Add(1)
	best, cur := arr[0], arr[0]
	for i := 1; i < len(arr); i++ {
		accesses.Add(1)
		cur = max(cur+arr[i], arr[i])
		best = max(best, cur)
	}
	return best
}

// MaxSubarrayRange returns the half-open range [lo, hi) of a maximum-sum
// non-empty subarray together with its sum. Any maximal range may be
// returned when several exist. It returns (0, 0, 0) for an empty slice.
func MaxSubarrayRange(arr []int) (lo, hi, sum int) {
	if len(arr) == 0 {
		return 0, 0, 0
	}
	accesses.Add(1)
	best, cur := arr[0], arr[0]
	lo, hi, start := 0, 1, 0
	for i := 1; i < len(arr); i++ {
		accesses.Add(1)
		if cur+arr[i] < arr[i] {
			cur, start = arr[i], i
		} else {
			cur += arr[i]
		}
		if cur > best {
			best, lo, hi = cur, start, i+1
		}
	}
	return lo, hi, best
}
