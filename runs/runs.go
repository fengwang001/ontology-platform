// Package runs 把码点序列切成最长游程，并提供次数的十进制读写。
//
// 本包不依赖工程中的其他包；次数支持大于 int64 的值（math/big）。
package runs

import (
	"math/big"
	"unicode/utf8"
)

// Split 按最长游程遍历 s 中的码点，对每个 (r, n) 调用 yield。
// 不做 Unicode 规范化：码点相同才合并。
func Split(s string, yield func(r rune, n int64)) {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		n := int64(1)
		rest := s[size:]
		for len(rest) > 0 {
			r2, size2 := utf8.DecodeRuneInString(rest)
			if r2 != r {
				break
			}
			n++
			rest = rest[size2:]
		}
		yield(r, n)
		s = rest
	}
}

// Count 逐位累加十进制次数，支持任意大小且不会溢出。
type Count struct {
	n   big.Int
	cnt int // 已输入的位数
}

// AddDigit 追加一个十进制位 d（0..9）。
func (c *Count) AddDigit(d byte) {
	c.n.Mul(&c.n, big.NewInt(10))
	c.n.Add(&c.n, big.NewInt(int64(d)))
	c.cnt++
}

// Len 返回已输入的位数。
func (c *Count) Len() int {
	return c.cnt
}

// IsZero 报告次数是否为 0（含 "0"、"00" 等）。
func (c *Count) IsZero() bool {
	return c.cnt > 0 && c.n.Sign() == 0
}

// IsOne 报告次数是否恰为 1。
func (c *Count) IsOne() bool {
	return c.cnt > 0 && c.n.Cmp(big.NewInt(1)) == 0
}

// Value 返回累计次数；未输入任何数字时为 0。
func (c *Count) Value() *big.Int {
	return new(big.Int).Set(&c.n)
}
