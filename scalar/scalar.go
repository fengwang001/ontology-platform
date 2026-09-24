// Package scalar 判定单个 Unicode 标量值（code point，排除代理区）。
// 本包不依赖工程内其他包，且不使用 unicode/utf8、unicode/utf16。
package scalar

const (
	// Max 是 Unicode 标量值上界（含）。
	Max = 0x10FFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是零宽不换行空格，流首用作字节序标记。
	BOM = 0xFEFF
	surrogateLo = 0xD800
	surrogateHi = 0xDFFF
)

// Valid 报告 r 是否为合法 Unicode 标量值：未越界且不在代理区。
func Valid(r rune) bool {
	return r >= 0 && r <= Max && !(r >= surrogateLo && r <= surrogateHi)
}

// Surrogate 报告 r 是否落在 UTF-16 代理区 D800..DFFF。
func Surrogate(r rune) bool { return r >= surrogateLo && r <= surrogateHi }

// HighSurrogate 报告 r 是否为高代理 D800..DBFF。
func HighSurrogate(r rune) bool { return r >= surrogateLo && r <= 0xDBFF }

// LowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= surrogateHi }

// FromSurrogatePair 把高/低代理对还原为标量值；不做合法性检查。
func FromSurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 + rune(lo)-0xDC00
}

// SurrogatePair 把 r（必须 ≥ 0x10000）编码为高、低代理。
func SurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return uint16(0xD800 + v>>10), uint16(0xDC00 + v&0x3FF)
}
