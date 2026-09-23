// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

// Rune 是一个 Unicode 码点。
type Rune = rune

const (
	// MaxRune 是 Unicode 码点上界。
	MaxRune = 0x10FFFF
	// SurrogateMin 高代理区起点。
	SurrogateMin = 0xD800
	// SurrogateMax 低代理区终点。
	SurrogateMax = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement Rune = 0xFFFD
	// BOM 是 U+FEFF。
	BOM Rune = 0xFEFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界、非负）。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && (r < SurrogateMin || r > SurrogateMax)
}

// IsSurrogate 报告 r 是否落在代理区 D800..DFFF。
func IsSurrogate(r Rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// InRange 报告闭区间 [lo,hi]（用于第二字节合法区间判定）。
func InRange(v, lo, hi byte) bool { return v >= lo && v <= hi }

// IsCont 报告 b 是否为 UTF-8 续写字节 10xxxxxx。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// DecodeSurrogatePair 将高/低代理组合成标量；输入非代理对时返回 false。
func DecodeSurrogatePair(hi, lo Rune) (Rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// EncodeSurrogatePair 将 r（必须 ≥ 0x10000）编为高、低代理。
func EncodeSurrogatePair(r Rune) (hi, lo Rune, ok bool) {
	if r < 0x10000 || r > MaxRune {
		return 0, 0, false
	}
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF, true
}
