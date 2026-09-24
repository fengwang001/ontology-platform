// Package bits 从 float64 的 IEEE 754 二进制表示中提取符号、阶码与尾数。
package bits

import "math"

// Parts 是 float64 三个字段的拆解结果。
type Parts struct {
	Neg  bool   // 符号位
	Exp  int    // 原始阶码字段，0..2047
	Mant uint64 // 原始尾数字段，低 52 位
}

// Decompose 用 math.Float64bits 取出 x 的符号、阶码与尾数。
func Decompose(x float64) Parts {
	b := math.Float64bits(x)
	return Parts{
		Neg:  b>>63 != 0,
		Exp:  int(b>>52) & 0x7ff,
		Mant: b & (1<<52 - 1),
	}
}

// Compose 由符号、阶码与尾数字段还原 float64，是 Decompose 的逆。
func Compose(p Parts) float64 {
	b := uint64(p.Exp&0x7ff)<<52 | p.Mant&(1<<52-1)
	if p.Neg {
		b |= 1 << 63
	}
	return math.Float64frombits(b)
}

// IsNaN 报告 x 是否为 NaN。
func IsNaN(x float64) bool {
	p := Decompose(x)
	return p.Exp == 0x7ff && p.Mant != 0
}

// IsInf 报告 x 是否为正或负无穷。
func IsInf(x float64) bool {
	p := Decompose(x)
	return p.Exp == 0x7ff && p.Mant == 0
}

// IsZero 报告 x 是否为 +0.0 或 -0.0。
func IsZero(x float64) bool {
	p := Decompose(x)
	return p.Exp == 0 && p.Mant == 0
}

// Int 返回 x 的精确整数分解：x = (-1)^neg * m * 2^e。
// 正规数 m 含隐含的 1（53 位），次正规数 m 为尾数本身。
// 调用方保证 x 为有限的非零值。
func Int(x float64) (neg bool, m uint64, e int) {
	p := Decompose(x)
	if p.Exp == 0 {
		return p.Neg, p.Mant, -1074
	}
	return p.Neg, p.Mant | 1<<52, p.Exp - 1075
}
