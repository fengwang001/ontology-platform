// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）。
//
// 标量值 = 码点区间 [0,0xD7FF] 与 [0xE000,0x10FFFF]；
// [0xD800,0xDFFF] 是代理区，0x10FFFF 以上越界。
// 本包不依赖其他包，不使用 unicode/utf8、unicode/utf16。
package scalar

// Max 是 Unicode 允许的最大码点。
const Max = 0x10FFFF

// SurrogateMin/Max 圈定代理区。
const (
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
)

// Valid 报告 r 是否为 Unicode 标量值（非代理、未越界）。
func Valid(r rune) bool {
	return r >= 0 && r < SurrogateMin || r > SurrogateMax && r <= Max
}

// Surrogate 报告 r 是否落在代理区。
func Surrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// HighSurrogate 报告 r 是否为高代理（前导代理）码元。
func HighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// LowSurrogate 报告 r 是否为低代理（尾随代理）码元。
func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// FromSurrogatePair 按 UTF-16 规则合并代理对为码点。
func FromSurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
}

// ToSurrogatePair 把 r（≥U+10000）拆成高、低代理码元。
func ToSurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}
