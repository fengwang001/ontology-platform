// Package runs 提供 Unicode 码点序列的最长游程切分与次数的任意精度十进制读写。
package runs

import (
	"math/big"
	"unicode/utf8"
)

// Run 表示符号 Rune 连续出现 N 次。
type Run struct {
	Rune rune
	N    Count
}

// Count 是无符号十进制次数的任意精度表示，永不溢出。
type Count struct{ n big.Int }

// NewCount 返回值为 n 的次数。
func NewCount(n uint64) Count {
	var c Count
	c.n.SetUint64(n)
	return c
}

// AppendDigit 追加一个十进制数位（0..9），返回 false 表示 b 不是数字。
func (c *Count) AppendDigit(b byte) bool {
	if b < '0' || b > '9' {
		return false
	}
	c.n.Mul(&c.n, big.NewInt(10))
	c.n.Add(&c.n, big.NewInt(int64(b-'0')))
	return true
}

// IsZero 报告次数是否为 0。
func (c Count) IsZero() bool { return c.n.Sign() == 0 }

// CmpUint64 比较次数与 n：-1 / 0 / 1。
func (c Count) CmpUint64(n uint64) int { return c.n.Cmp(big.NewInt(0).SetUint64(n)) }

// Uint64 返回低 64 位；仅在 CmpUint64 确认可容纳时安全使用。
func (c Count) Uint64() uint64 { return c.n.Uint64() }

// String 返回无前导零的十进制表示（0 为 "0"）。
func (c Count) String() string { return c.n.String() }

// MulUint64 返回 c*n。
func (c Count) MulUint64(n uint64) Count {
	var out Count
	out.n.Mul(&c.n, big.NewInt(0).SetUint64(n))
	return out
}

// AddUint64 返回 c+n。
func (c Count) AddUint64(n uint64) Count {
	var out Count
	out.n.Add(&c.n, big.NewInt(0).SetUint64(n))
	return out
}

// SubUint64 返回 c-n；要求 c >= n。
func (c Count) SubUint64(n uint64) Count {
	var out Count
	out.n.Sub(&c.n, big.NewInt(0).SetUint64(n))
	return out
}

// SetZero 将次数置为 0。
func (c *Count) SetZero() { c.n.SetUint64(0) }

// Split 将合法 UTF-8 字符串切成最长游程并回调；回调返回 false 时停止。
func Split(s string, fn func(Run) bool) {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		n := uint64(1)
		rest := s[size:]
		for len(rest) > 0 {
			r2, size2 := utf8.DecodeRuneInString(rest)
			if r2 != r {
				break
			}
			n++
			rest = rest[size2:]
		}
		if !fn(Run{Rune: r, N: NewCount(n)}) {
			return
		}
		s = rest
	}
}
