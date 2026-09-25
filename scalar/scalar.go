// Package scalar 判定单个 Unicode 标量值的合法区间。
package scalar

// Rune 是一个 Unicode 码位。
type Rune = rune

const (
	// MaxRune 是 Unicode 码位上界。
	MaxRune = 0x10FFFF
	// SurrogateMin 是高代理区起点。
	SurrogateMin = 0xD800
	// SurrogateMax 是低代理区终点。
	SurrogateMax = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是 U+FEFF。
	BOM = 0xFEFF
)

// IsScalar 报告 r 是否为合法标量值（非代理、不越界、非负）。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateMin && r <= SurrogateMax)
}

// IsHighSurrogate 报告 r 是否为高代理码元。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理码元。
func IsLowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// FromSurrogatePair 由高、低代理还原码位；入参非法时返回 -1。
func FromSurrogatePair(hi, lo Rune) Rune {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return -1
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// ToSurrogatePair 把 r（必须 ≥ 0x10000）拆成高、低代理；非法时 hi<0。
func ToSurrogatePair(r Rune) (hi, lo Rune) {
	if r < 0x10000 || r > MaxRune {
		return -1, -1
	}
	r -= 0x10000
	return 0xD800 + (r >> 10), 0xDC00 + (r & 0x3FF)
}
