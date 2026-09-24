// Package scalar 判定单个 Unicode 标量值：合法区间、代理区、越界。
package scalar

// Rune 是一个 Unicode 码点（未判定是否为标量值）。
type Rune = rune

const (
	MaxRune     = 0x10FFFF // Unicode 码点上限
	Surrogate   = 0xD800   // 代理区起点
	SurEnd      = 0xDFFF   // 代理区终点
	BOM         = 0xFEFF   // 零宽不换行空格 / BOM
	Replacement = 0xFFFD   // U+FFFD
)

// IsScalar 报告 r 是否为 Unicode 标量值（非代理且不越界，含 U+10FFFF）。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= Surrogate && r <= SurEnd)
}

// IsSurrogate 报告 r 是否落在高/低代理区 [D800,DFFF]。
func IsSurrogate(r Rune) bool { return r >= Surrogate && r <= SurEnd }

// IsHighSurrogate 报告 r 是否为高代理 [D800,DBFF]。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理 [DC00,DFFF]。
func IsLowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair 把高代理 h 与低代理 l 组合为码点；不合法返回 false。
func SurrogatePair(h, l Rune) (Rune, bool) {
	if !IsHighSurrogate(h) || !IsLowSurrogate(l) {
		return 0, false
	}
	return 0x10000 + (h-0xD800)<<10 + (l - 0xDC00), true
}

// SplitSurrogate 返回 r（r>=10000）的高、低代理；非增补平面返回 false。
func SplitSurrogate(r Rune) (hi, lo Rune, ok bool) {
	if r < 0x10000 || r > MaxRune {
		return 0, 0, false
	}
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF, true
}

// IsInRange 报告 r 是否落在 [lo,hi]。
func IsInRange(r Rune, lo, hi Rune) bool { return r >= lo && r <= hi }
