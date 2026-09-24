// Package runs 把码点序列切成最长游程，并提供游程次数的十进制读写。
// 本包不依赖工程内其他包。
package runs

import (
	"math"
	"strconv"
)

// Run 是一个最长游程：符号 Sym 连续出现 N 次（N >= 1）。
type Run struct {
	Sym rune
	N   uint64
}

// Split 把 s 切成最长游程序列；不做任何 Unicode 规范化，
// 例如 U+00E9 与 'e'+U+0301 是不同的码点序列。
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
	}
	return out
}

// AppendCount 把次数 n 按十进制（无符号、无前导零）追加到 dst。
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount 解析十进制次数 s（必须只含数字），返回值不超过 ceiling：
// 真实值 >= ceiling 时截断返回 ceiling。饱和而非回绕，因此任意长的
// 数字串都不会整数溢出，调用方以 ceiling 表达「超出即拒绝」的上限。
func ParseCount(s string, ceiling uint64) uint64 {
	n := uint64(0)
	for i := 0; i < len(s); i++ {
		if n > (math.MaxUint64-9)/10 {
			return ceiling
		}
		n = n*10 + uint64(s[i]-'0')
		if n >= ceiling {
			return ceiling
		}
	}
	return n
}
