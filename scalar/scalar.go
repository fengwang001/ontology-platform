// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

// Rune 是一个 Unicode 码点；合法标量值排除代理区与越界值。
type Rune = rune

const (
	MaxRune     = 0x10FFFF
	SurHighLo   = 0xD800
	SurHighHi   = 0xDBFF
	SurLowLo    = 0xDC00
	SurLowHi    = 0xDFFF
	Replacement = 0xFFFD
	BOM         = 0xFEFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区 D800..DFFF。
func IsSurrogate(r rune) bool { return r >= SurHighLo && r <= SurLowHi }

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r rune) bool { return r >= SurHighLo && r <= SurHighHi }

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r rune) bool { return r >= SurLowLo && r <= SurLowHi }

// FromSurrogatePair 把高/低代理还原为标量；调用方须保证代理合法。
func FromSurrogatePair(hi, lo rune) rune {
	return 0x10000 + (hi-SurHighLo)<<10 + (lo - SurLowLo)
}

// SurrogatePair 把 r（≥10000）编码为高、低代理。
func SurrogatePair(r rune) (hi, lo rune) {
	r -= 0x10000
	return SurHighLo + r>>10, SurLowLo + r&0x3FF
}
