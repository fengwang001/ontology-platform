// Package ch 提供计数布隆过滤器的哈希函数：第 j 个哈希把非负整数 key
// 映射到计数器下标 h_j(x) = (j · x) mod m。本包不依赖任何其他包。
package ch

// Index 返回第 j 个哈希函数（j = 1..k）对 key 的命中计数器下标。
// 用无符号乘法避免 int64 溢出的实现相关行为；j、m 由调用方保证为正。
func Index(j int, key int64, m int) int {
	return int((uint64(j) * uint64(key)) % uint64(m))
}

// Indices 返回 key 在 k 个哈希函数下的全部命中下标（j = 1..k）。
func Indices(k int, key int64, m int) []int {
	idx := make([]int, k)
	for j := 1; j <= k; j++ {
		idx[j-1] = Index(j, key, m)
	}
	return idx
}
