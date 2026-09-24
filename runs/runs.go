// Package runs 把码点序列切成最长游程，并提供次数的十进制读写。
// 它不依赖工程内其他包。
package runs

import (
	"math"
	"strconv"
	"unicode/utf8"
)

// MaxCount 是允许的最大游程次数：这是 Go 字符串长度能够编码出来的天然上界，
// 也保证输出展开时不会发生整数溢出。
const MaxCount = uint64(math.MaxInt64)

// Each 以最长游程遍历 s：对每个互不相同的相邻码点回调一次，r 为码点，n 为次数。
// 非法 UTF-8 字节按 utf8.DecodeRuneInString 的约定以 RuneError 逐字节交付；
// 连续的非法字节因此会合并为同一游程，符合“按码点”的切分定义。
func Each(s string, fn func(r rune, n uint64) bool) {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		n := uint64(1)
		for rest := s[size:]; len(rest) > 0; {
			r2, size2 := utf8.DecodeRuneInString(rest)
			if r2 != r {
				break
			}
			n++
			size += size2
			rest = rest[size2:]
		}
		if !fn(r, n) {
			return
		}
		s = s[size:]
	}
}

// FormatCount 把次数写成无前导零的无符号十进制。
func FormatCount(n uint64) string {
	return strconv.FormatUint(n, 10)
}

// AppendDigit 在已有数值 acc 后追加一位十进制数字 d。
// ok 为 false 表示追加后超过 MaxCount（溢出），调用方必须拒绝该次数。
func AppendDigit(acc uint64, d byte) (n uint64, ok bool) {
	const base = 10
	if acc > (MaxCount-uint64(d-'0'))/base {
		return 0, false
	}
	return acc*base + uint64(d-'0'), true
}
