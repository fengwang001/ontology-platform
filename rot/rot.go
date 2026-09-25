// Package rot 在旋转排序数组（某严格升序数组的循环右移）上做 O(log n) 查找。
package rot

import "sync/atomic"

// keyCmps 非导出计数器：仅统计「关键字比较」（target 与数组元素的比较），
// 元素间的结构性比较不计入。用 atomic 保证并发调用 Search 时 -race 干净。
var keyCmps atomic.Int64

// Comparisons 返回所有 Search 调用累计的关键字比较次数（测试与诊断用）。
func Comparisons() int64 { return keyCmps.Load() }

// ResetComparisons 将关键字比较计数器清零（测试与诊断用）。
func ResetComparisons() { keyCmps.Store(0) }

// Search 返回 target 在 nums 中的下标；未命中返回 -1。
// nums 应为某严格升序数组的循环右移（可用 arr.Validate 校验）；target 出现
// 多次时返回其中任一下标。输入不合法时行为自洽：不 panic，返回 -1 或
// target 的某一下标。Search 是纯函数，并发调用互不影响。
func Search(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		// 先判断哪半边严格有序，再判断 target 是否落在有序半的范围内。
		if nums[mid] >= nums[lo] { // [lo,mid] 严格有序
			if inRange(nums[lo], nums[mid], target) {
				hi = mid
			} else {
				lo = mid + 1
			}
		} else { // [mid,hi] 严格有序
			if inRange(nums[mid], nums[hi], target) {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
	}
	keyCmps.Add(1)
	if lo < len(nums) && nums[lo] == target {
		return lo
	}
	return -1
}

// inRange 判断 target 是否落在严格有序区间的闭区间 [a,b] 内，
// 并按实际发生的关键字比较次数计数。
func inRange(a, b, target int) bool {
	keyCmps.Add(1)
	if target < a {
		return false
	}
	keyCmps.Add(1)
	return target <= b
}
