// Package key 定义键的全序与确定性层高导出，不依赖其他包。
package key

import "math/bits"

// Key 是跳表元素的键，底层为 int64，按数值全序。
type Key int64

// Compare 返回 a 与 b 的全序关系：-1、0 或 1。
func Compare(a, b Key) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Equal 判定两键相等。
func Equal(a, b Key) bool { return a == b }

// Level 由键确定性地导出层高（>=1）：对键做位混合后取末尾零位数加一。
// 相同的键必然得到相同层高；P(Level >= k) = 2^(1-k)。
func (k Key) Level() int {
	return bits.TrailingZeros64(mix64(uint64(k) + 0x9e3779b97f4a7c15)) + 1
}

// mix64 是 splitmix64 末轮混合器，雪崩充分、输出近似均匀。
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
