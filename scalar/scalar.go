// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

// MaxRune 是 Unicode 标量值上界。
const MaxRune = 0x10FFFF

// Surrogate 判断 r 是否落在 UTF-16 代理区 U+D800..U+DFFF。
func Surrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// Valid 判断 r 是否为合法 Unicode 标量值（非负、不越界、非代理）。
func Valid(r rune) bool { return r >= 0 && r <= MaxRune && !Surrogate(r) }

// Recombine 由一对合法高/低代理还原辅助平面标量。
func Recombine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi)-0xD800)<<10 | (rune(lo) - 0xDC00)
}

// Surrogates 把辅助平面标量拆成高/低代理。
func Surrogates(r rune) (hi, lo uint16) {
	r -= 0x10000
	return uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)
}
