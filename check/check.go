// Package check 提供朴素线性扫描参照，用于对拍 rot 的二分实现。
package check

// LinearSearch 线性扫描，返回 target 首次出现的下标，未命中或为空时返回 -1。
func LinearSearch(nums []int, target int) int {
	for i, v := range nums {
		if v == target {
			return i
		}
	}
	return -1
}
