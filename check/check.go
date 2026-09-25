// Package check 提供朴素线性扫描参照实现，供测试与 rot.Search 对拍。
package check

// Linear 返回 target 首次出现的下标，未命中返回 -1。
func Linear(nums []int, target int) int {
	for i, v := range nums {
		if v == target {
			return i
		}
	}
	return -1
}
