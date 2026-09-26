// Package hashk 计算布隆过滤器的 k 个哈希位置。
// 不依赖任何其他包。
package hashk

// H1 返回 (Σ byte_i) mod m。要求 m >= 1。
func H1(x []byte, m int) int {
	sum := 0
	for _, b := range x {
		sum += int(b)
	}
	return sum % m
}

// H2 返回 1 + ((Σ byte_i·(i+1)) mod (m-1))，下标 i 从 0 起。
// m == 1 时除数退化为 1（mod 1 恒为 0），保证不除零。
func H2(x []byte, m int) int {
	div := m - 1
	if div < 1 {
		div = 1
	}
	sum := 0
	for i, b := range x {
		sum += int(b) * (i + 1)
	}
	return 1 + sum%div
}

// Positions 返回 x 的 k 个位置：pos_j = (h1 + j·h2) mod m，j = 0..k-1。
// 要求 m >= 1、k >= 1。
func Positions(x []byte, m, k int) []int {
	h1, h2 := H1(x, m), H2(x, m)
	pos := make([]int, k)
	for j := 0; j < k; j++ {
		pos[j] = (h1 + j*h2) % m
	}
	return pos
}
