// Package check provides an O(n^2) brute-force reference for the
// maximum non-empty subarray problem, used to validate package kad.
package check

// NaiveMaxSum enumerates all non-empty subarrays and returns the
// maximum sum. It returns 0 for an empty slice.
func NaiveMaxSum(arr []int) int {
	_, _, sum := NaiveMaxRange(arr)
	return sum
}

// NaiveMaxRange enumerates all non-empty subarrays and returns the
// half-open range [lo, hi) and sum of the first maximum found.
// It returns (0, 0, 0) for an empty slice.
func NaiveMaxRange(arr []int) (lo, hi, sum int) {
	if len(arr) == 0 {
		return 0, 0, 0
	}
	lo, hi, sum = 0, 1, arr[0]
	for i := 0; i < len(arr); i++ {
		cur := 0
		for j := i; j < len(arr); j++ {
			cur += arr[j]
			if cur > sum {
				lo, hi, sum = i, j+1, cur
			}
		}
	}
	return lo, hi, sum
}
