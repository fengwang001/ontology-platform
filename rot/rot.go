// Package rot 在旋转排序数组上做 O(log n) 二分查找。
package rot

import "sync/atomic"

// comparisons 为非导出计数器，只计关键比较（target 与数组元素之比）。
var comparisons atomic.Int64

// Comparisons 返回自上次 ResetComparisons 以来累计的关键比较次数。
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons 清零比较计数器。
func ResetComparisons() { comparisons.Store(0) }

func leq(a, b int) bool { comparisons.Add(1); return a <= b }

func lt(a, b int) bool { comparisons.Add(1); return a < b }

func eq(a, b int) bool { comparisons.Add(1); return a == b }

// Search 返回 target 在 nums 中的下标，未命中返回 -1。
// nums 须为合法的严格旋转排序数组（合法性由 arr.Validate 判定）。
func Search(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if nums[lo] <= nums[mid] { // [lo,mid] 严格有序
			if leq(nums[lo], target) && leq(target, nums[mid]) {
				hi = mid
			} else {
				lo = mid + 1
			}
		} else { // [mid+1,hi] 严格有序
			if lt(nums[mid], target) && leq(target, nums[hi]) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
	}
	if lo <= hi && eq(nums[lo], target) {
		return lo
	}
	return -1
}
