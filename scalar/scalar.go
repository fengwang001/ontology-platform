// Package scalar 判定单个 Unicode 标量值的合法区间。
package scalar

// Rune 是一个 Unicode 码点（不保证是标量值）。
type Rune = uint32

const (
	// Max 是 Unicode 码点上界。
	Max Rune = 0x10FFFF
	// SurrogateLo 是代理区下界。
	SurrogateLo Rune = 0xD800
	// SurrogateHi 是代理区上界。
	SurrogateHi Rune = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement Rune = 0xFFFD
	// BOM 是 U+FEFF。
	BOM Rune = 0xFEFF
)

// Valid 报告 r 是否为合法 Unicode 标量值：未越界且不在代理区。
func Valid(r Rune) bool {
	return r <= Max && !(r >= SurrogateLo && r <= SurrogateHi)
}

// Surrogate 报告 r 是否落在 UTF-16 代理区。
func Surrogate(r Rune) bool {
	return r >= SurrogateLo && r <= SurrogateHi
}

// HighSurrogate 报告 r 是否为高代理（引导代理）。
func HighSurrogate(r Rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// LowSurrogate 报告 r 是否为低代理（尾随代理）。
func LowSurrogate(r Rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// CombineSurrogate 把高、低代理还原为辅助平面标量；非代理对时返回 false。
func CombineSurrogate(hi, lo Rune) (Rune, bool) {
	if !HighSurrogate(hi) || !LowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// SplitSurrogate 把辅助平面标量拆成高、低代理；BMP 码点返回 false。
func SplitSurrogate(r Rune) (hi, lo Rune, ok bool) {
	if r < 0x10000 || r > Max {
		return 0, 0, false
	}
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF, true
}
