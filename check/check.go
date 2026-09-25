// Package check 提供 O(n^2) 朴素参照实现，用于对照验证 cnt。
package check

// NaiveCount 用双重循环统计逆序对数量，作为正确性参照。
func NaiveCount(a []int) int64 {
	var count int64
	for i := 0; i < len(a); i++ {
		for j := i + 1; j < len(a); j++ {
			if a[i] > a[j] {
				count++
			}
		}
	}
	return count
}
