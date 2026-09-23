// Package bits 从 float64 取出符号、阶码、尾数（仅用 math.Float64bits）。
package bits

import "math"

const (
	signMask uint64 = 1 << 63
	expMask  uint64 = 0x7ff << 52
	manMask  uint64 = 1<<52 - 1
)

// Kind 是按位分类。
type Kind uint8

const (
	KindZero Kind = iota
	KindNormal
	KindSubnormal
	KindInf
	KindNaN
)

// Parts 是 float64 的按位分解。
type Parts struct {
	Neg  bool
	Kind Kind
	Bits uint64
	// Mant 为尾数整数值；Exp 为二进指数，使值的绝对值等于 Mant * 2^Exp。
	// 零、Inf、NaN 时 Mant 为 0。
	Mant uint64
	Exp  int
}

// Split 按 IEEE 754 双精度布局分解 x。
func Split(x float64) Parts {
	b := math.Float64bits(x)
	p := Parts{Bits: b, Neg: b&signMask != 0}
	expField := int((b & expMask) >> 52)
	frac := b & manMask
	switch {
	case expField == 0x7ff:
		if frac == 0 {
			p.Kind = KindInf
		} else {
			p.Kind = KindNaN
		}
	case expField == 0:
		if frac == 0 {
			p.Kind = KindZero
		} else {
			p.Kind = KindSubnormal
			p.Mant = frac
			p.Exp = -1074 // 无隐含位
		}
	default:
		p.Kind = KindNormal
		p.Mant = frac | 1<<52
		p.Exp = expField - 1023 - 52
	}
	return p
}
