// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

// RuneError 是替换字符 U+FFFD。
const RuneError rune = 0xFFFD

// MaxRune 是 Unicode 标量值上界 U+10FFFF。
const MaxRune rune = 0x10FFFF

// SurrogateMin / SurrogateMax 是高、低代理码点区间。
const (
	SurrogateMin rune = 0xD800
	SurrogateMax rune = 0xDFFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r < SurrogateMin || r > SurrogateMax && r <= MaxRune
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate 报告 r 是否为高代理码元 U+D800..U+DBFF。
func IsHighSurrogate(r rune) bool { return r >= SurrogateMin && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理码元 U+DC00..U+DFFF。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= SurrogateMax }

// DecodeSurrogatePair 把高、低代理码元还原为标量值（调用前须自行判定区间）。
func DecodeSurrogatePair(high, low rune) rune {
	return 0x10000 + (high-SurrogateMin)<<10 + (low - 0xDC00)
}

// EncodeSurrogatePair 把 ≥U+10000 的标量编码为高、低代理码元。
func EncodeSurrogatePair(r rune) (high, low rune) {
	r -= 0x10000
	return SurrogateMin + r>>10, 0xDC00 + r&0x3FF
}
