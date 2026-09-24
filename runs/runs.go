// Package runs 把码点序列切成最长游程，并提供不溢出的十进制次数读写。
// 不做任何 Unicode 规范化：é 与 e+U+0301 是不同的码点序列。
package runs

import (
	"math"
	"unicode/utf8"
)

// Run 是一个最长游程：符号 Sym 连续出现 N 次。
type Run struct {
	Sym rune
	N   uint64
}

// Split 把 s 切成最长游程序列；相邻游程的符号必不相同。
func Split(s string) []Run {
	var out []Run
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
		s = s[size:]
	}
	return out
}

// AppendCount 把 n 以十进制、无符号、无前导零的形式追加到 dst。
func AppendCount(dst []byte, n uint64) []byte {
	var tmp [20]byte
	i := len(tmp)
	for {
		i--
		tmp[i] = byte('0' + n%10)
		if n /= 10; n == 0 {
			break
		}
	}
	return append(dst, tmp[i:]...)
}

// Accum 饱和累加一个十进制非负整数：超过 math.MaxUint64 时置饱和位，
// 绝不回绕，因此不会静默得到错误结果。饱和值必大于任何输出预算。
type Accum struct {
	v   uint64
	sat bool
	n   int
}

// Add 累加一位十进制数字 d（必须在 '0'..'9' 之间）。
func (a *Accum) Add(d byte) {
	a.n++
	if a.sat {
		return
	}
	if a.v > (math.MaxUint64-9)/10 {
		a.sat = true
		return
	}
	a.v = a.v*10 + uint64(d-'0')
}

// Len 返回累加的数字位数。
func (a *Accum) Len() int { return a.n }

// Value 返回累加值；ok 为 false 表示已饱和（真实值 ≥ 2^64）。
func (a *Accum) Value() (v uint64, ok bool) { return a.v, !a.sat }
