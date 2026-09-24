// Package bits 把 float64 拆成符号、阶码、尾数，并能按位重组。
package bits

import "math"

// Kind 是按位模式划分的粗类别。
type Kind uint8

const (
	Zero      Kind = iota // 正负零
	Subnormal             // 次正规数
	Normal                // 正规数
	Inf                   // 正负无穷
	NaN                   // 非数
)

// Parts 是 float64 的位分解结果。
type Parts struct {
	Neg  bool   // 符号位
	Kind Kind   // 粗类别
	Exp  int    // 有效数字最低位的权指数：值 = Mant * 2^Exp
	Mant uint64 // 含隐含位的 53 位尾数；次正规可能少于 53 位
}

// Split 用 math.Float64bits 取出位模式并分解。
func Split(x float64) Parts {
	b := math.Float64bits(x)
	p := Parts{Neg: b>>63 == 1}
	f, m := b&0x7ff0000000000000, b&0xfffffffffffff
	switch {
	case f == 0 && m == 0:
		p.Kind = Zero
	case f == 0:
		p.Kind, p.Mant = Subnormal, m
	case f == 0x7ff0000000000000 && m == 0:
		p.Kind = Inf
	case f == 0x7ff0000000000000:
		p.Kind = NaN
	default:
		p.Kind, p.Mant = Normal, m|1<<52
	}
	switch p.Kind {
	case Normal:
		p.Exp = int(f>>52) - 1023 - 52
	case Subnormal:
		p.Exp = -1022 - 52
	}
	return p
}

// Join 按符号、阶码、53 位尾数重组正规/次正规 float64 的位模式；溢出时为无穷。
func Join(neg bool, exp int, mant uint64) uint64 {
	var s uint64
	if neg {
		s = 1 << 63
	}
	if exp > 1023-52 {
		return s | 0x7ff0000000000000
	}
	if exp >= -1022-52 && mant&(1<<52) != 0 {
		return s | uint64(exp+1023+52)<<52 | mant&(1<<52-1)
	}
	shift := uint((-1022 - 52) - exp)
	return s | mant>>shift
}
