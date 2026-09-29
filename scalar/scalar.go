// Package scalar 判定单个 Unicode 标量值与码元的合法区间。
package scalar

const ReplacementRune = '\uFFFD'

// IsScalar 报告 r 是否为 Unicode 标量值（排除代理区 D800..DFFF）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= 0x10FFFF && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

// IsHighSurrogate 报告码元是否为高代理 D800..DBFF。
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate 报告码元是否为低代理 DC00..DFFF。
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// SurrogatePair 把高、低代理还原为标量值（调用方须自行保证代理合法）。
func SurrogatePair(hi, lo uint16) rune {
	return rune(uint32(hi-0xD800)<<10|uint32(lo-0xDC00)) + 0x10000
}

// EncodeSurrogates 把 r（须 ≥ U+10000）编为高、低代理码元。
func EncodeSurrogates(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}
