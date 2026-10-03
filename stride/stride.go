// Package stride 提供同余类对齐与步长模式计算。
package stride

// AlignUp 返回不小于 x 且模 m 余 c 的最小整数。
// 要求 m >= 1 且 0 <= c < m，x 为非负整数。
func AlignUp(x, c, m int64) int64 {
	r := x % m
	if r < 0 {
		r += m
	}
	if r <= c {
		return x + (c - r)
	}
	return x + (m - r + c)
}

// Mode 返回活跃系统数为 active 时的签发步长：
// 仅 1 个活跃系统时步长为 1，不少于 2 个时步长为 m。
func Mode(active int, m int64) int64 {
	if active >= 2 {
		return m
	}
	return 1
}
