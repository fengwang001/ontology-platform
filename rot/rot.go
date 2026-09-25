// Package rot 在旋转排序数组（严格递增序列循环右移 k 位）上以 O(log n)
// 查找目标值。每轮二分先判断哪一半严格有序，再判断 target 是否落在
// 有序那半的值域内，避免普通二分在旋转数组上的漏查。
package rot

import "sync/atomic"

// comparisons 非导出计数器：与 target 的关键比较次数（三态比较计 1 次），
// 用 atomic 保证并发调用 Search 时 -race 干净。
var comparisons atomic.Int64

// Comparisons 返回累计关键比较次数（供测试断言对数上界）。
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons 清零计数器。
func ResetComparisons() { comparisons.Store(0) }

// cmpTarget 三态比较 v 与 target：v<target 得 -1，相等得 0，v>target 得 1。
func cmpTarget(v, target int) int {
	comparisons.Add(1)
	switch {
	case v < target:
		return -1
	case v > target:
		return 1
	default:
		return 0
	}
}

// Search 返回 target 在 nums 中的下标，未命中返回 -1。
// nums 应为严格递增序列的循环右移；空切片返回 -1。
func Search(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		c := cmpTarget(nums[mid], target)
		if c == 0 {
			return mid
		}
		if nums[lo] <= nums[mid] { // 左半 [lo,mid] 严格有序
			// c>0 即 target<nums[mid]，只需再确认 target>=nums[lo]。
			if c > 0 && cmpTarget(nums[lo], target) <= 0 {
				hi = mid - 1
			} else {
				lo = mid + 1
			}
		} else { // 右半 [mid,hi] 严格有序
			// c<0 即 target>nums[mid]，只需再确认 target<=nums[hi]。
			if c < 0 && cmpTarget(nums[hi], target) >= 0 {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
	}
	return -1
}
