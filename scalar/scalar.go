// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
// 标量值 = 0..0x10FFFF 中除去代理区 D800..DFFF 的码点。
package scalar

// Rune 是一个 Unicode 码点；只有 Valid 为 true 时才是标量值。
type Rune struct {
	Value rune
	Valid bool
}

const (
	MaxRune     = 0x10FFFF
	SurrogateHi = 0xD800
	SurrogateLo = 0xDFFF
	Replacement = 0xFFFD
	BOM         = 0xFEFF
)

// Valid 报告 r 是否为合法标量值（非负、不越界、不在代理区）。
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateHi && r <= SurrogateLo)
}

// Surrogate 报告 r 是否落在 UTF-16 代理区。
func Surrogate(r rune) bool { return r >= SurrogateHi && r <= SurrogateLo }

// HighSurrogate 报告 16 位值 u 是否为高代理 D800..DBFF。
func HighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// LowSurrogate 报告 16 位值 u 是否为低代理 DC00..DFFF。
func LowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// CombineSurrogates 把高代理 hi 与低代理 lo 组合成增补平面码点。
func CombineSurrogates(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}
