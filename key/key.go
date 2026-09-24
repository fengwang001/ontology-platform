// Package key 定义键的全序与由键确定性导出的层高。
package key

import "math/bits"

// MaxLevel 是层高的硬上限。
const MaxLevel = 64

// K 是跳表的键类型，按数值全序排列。
type K uint64

// Compare 返回 -1/0/+1，表示 a 小于/等于/大于 b。
func Compare(a, b K) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Equal 判定两键相等。
func Equal(a, b K) bool { return a == b }

// Level 由键确定性地导出层高：对键做 FNV-1a 64 位散列，
// 取结果末尾零比特数加一。同一键必然得到同一层高。
func Level(k K) int {
	h := uint64(14695981039346656037)
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(k >> (8 * i)))
		h *= 1099511628211
	}
	if h == 0 {
		return MaxLevel
	}
	l := bits.TrailingZeros64(h) + 1
	if l > MaxLevel {
		l = MaxLevel
	}
	return l
}
