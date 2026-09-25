// Package check 提供旋转排序数组查找的朴素参照实现。
package check

// Linear 线性扫描参照：返回首个等于 target 的下标，未命中返回 -1。
func Linear(nums []int, target int) int {
	for i, v := range nums {
		if v == target {
			return i
		}
	}
	return -1
}

// RotatedInts 返回长度 n、在 k 处循环右移的严格递增旋转数组（测试造数用）。
func RotatedInts(n, k int) []int {
	base := make([]int, n)
	for i := range base {
		base[i] = i
	}
	return append(append([]int{}, base[k:]...), base[:k]...)
}

// NaiveBinary 是事故中的错误实现：不判哪半有序，直接拿 nums[mid] 与 target 定向。
func NaiveBinary(nums []int, target int) int {
	for lo, hi := 0, len(nums)-1; lo <= hi; {
		mid := (lo + hi) / 2
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
