// Package rot 在被循环右移过的严格升序数组上做 O(log n) 二分查找。
package rot

import "sync/atomic"

// comparisons 记录最近一次 Search 的元素探测比较次数（供测试断言上界）。
var comparisons atomic.Int64

// Search 返回 target 在 nums 中的下标，未命中返回 -1。
// nums 须为严格升序数组经循环右移得到（元素互不相同）；空数组返回 -1。
func Search(nums []int, target int) int {
	lo, hi, count := 0, len(nums)-1, 0
	defer func() { comparisons.Store(int64(count)) }()

	for lo <= hi {
		mid := lo + (hi-lo)/2
		count++ // 命中探测：nums[mid] 与 target
		if nums[mid] == target {
			return mid
		}

		count++                    // 半边有序性判定
		if nums[lo] <= nums[mid] { // 左半 [lo,mid] 严格有序
			if nums[lo] <= target && target < nums[mid] {
				hi = mid - 1
			} else {
				lo = mid + 1
			}
		} else { // 右半 [mid,hi] 严格有序
			if nums[mid] < target && target <= nums[hi] {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
	}
	return -1
}

// LastComparisons 返回最近一次 Search 的元素探测比较次数。
func LastComparisons() int { return int(comparisons.Load()) }

// NaiveSearch 是事故中的错误写法：不判断哪半边有序，
// 直接像普通二分那样比较 nums[mid] 与 target，仅供测试钉住漏查行为。
func NaiveSearch(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if nums[mid] == target {
			return mid
		}
		if target < nums[mid] {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	return -1
}
