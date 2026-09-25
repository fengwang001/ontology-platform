// Package check provides an O(n^2) brute-force reference implementation
// used to validate the Kadane algorithm in package kad.
package check

// NaiveMaxSum enumerates every non-empty subarray and returns the max sum.
// It returns 0 for an empty slice.
func NaiveMaxSum(arr []int) int {
	_, _, sum := NaiveMaxRange(arr)
	return sum
}

// NaiveMaxRange enumerates all non-empty subarrays and returns lo, hi
// (inclusive) and the sum of a maximum-sum one (first found wins ties).
func NaiveMaxRange(arr []int) (lo, hi, sum int) {
	if len(arr) == 0 {
		return 0, -1, 0
	}
	lo, hi, sum = 0, 0, arr[0]
	for i := 0; i < len(arr); i++ {
		cur := 0
		for j := i; j < len(arr); j++ {
			cur += arr[j]
			if cur > sum {
				lo, hi, sum = i, j, cur
			}
		}
	}
	return lo, hi, sum
}
