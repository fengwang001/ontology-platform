// Package stride 提供同余类对齐与步长模式计算。
package stride

// MaxID 是全局编号上限。
const MaxID int64 = 1_000_000_000_000_000

// AlignUp 返回不小于 x 且模 m 余 c 的最小整数，要求 0 <= c < m。
func AlignUp(x, c, m int64) int64 {
	r := x % m
	if r < 0 {
		r += m
	}
	return x + (c-r+m)%m
}

// Of 返回活跃系统数为 active 时的签发步长：1 个活跃系统时步长为 1，
// 不少于 2 个时步长为 m。
func Of(active int, m int64) int64 {
	if active <= 1 {
		return 1
	}
	return m
}

// Class 返回系统 s（1 起）的同余类 c_s = s-1。
func Class(s int) int64 {
	return int64(s - 1)
}
