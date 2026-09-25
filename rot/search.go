// Package rot 在被循环右移过的严格递增数组上做 O(log n) 二分查找。
package rot

// Search 返回 target 在 nums 中的下标，未命中或 nums 为空时返回 -1。
// nums 应为严格递增数组经一次循环右移的结果（无旋转也算）。
func Search(nums []int, target int) int {
	idx, _ := searchStats(nums, target)
	return idx
}

// SearchWithCount 与 Search 行为一致，额外返回本次查找的元素比较次数。
// 计数器为调用栈内的局部变量，纯函数可安全并发使用。
func SearchWithCount(nums []int, target int) (idx, comparisons int) {
	return searchStats(nums, target)
}

func searchStats(nums []int, target int) (idx, comparisons int) {
	if len(nums) == 0 {
		return -1, 0
	}
	// 第一段：找最小元素下标（旋转点）。nums[mid] > nums[hi]
	// 说明右半含跌落点、不严格有序，旋转点在右侧；否则左半有序、向左收缩。
	lo, hi := 0, len(nums)-1
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		comparisons++
		if nums[mid] > nums[hi] {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	pivot := lo
	// 第二段：旋转点把数组切成两段严格有序区间，先按端点
	// 判断 target 落在哪一段（一次比较），再在该段做普通二分。
	lo, hi = 0, len(nums)-1
	comparisons++
	if target >= nums[pivot] && target <= nums[len(nums)-1] {
		lo = pivot
	} else {
		hi = pivot - 1
	}
	if hi < 0 {
		return -1, comparisons
	}
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		comparisons++
		switch {
		case nums[mid] == target:
			return mid, comparisons
		case nums[mid] < target:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return -1, comparisons
}
