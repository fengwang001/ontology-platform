// Package check 提供朴素线性扫描参照，用于对拍 rot 的正确性。
package check

// LinearSearch 逐个扫描返回 target 首次出现的下标，未命中返回 -1。
// 空数组返回 -1。
func LinearSearch(nums []int, target int) int {
	for i, v := range nums {
		if v == target {
			return i
		}
	}
	return -1
}
