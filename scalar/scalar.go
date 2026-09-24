// Package scalar 判定单个 Unicode 标量值（Unicode scalar value）的合法性。
// 本包不依赖工程内任何其他包。
package scalar

// Rune 是一个 Unicode 码点。
type Rune = int32

const (
	// Max 是 Unicode 码点上限。
	Max Rune = 0x10FFFF
	// SurrogateMin 是代理区起点。
	SurrogateMin Rune = 0xD800
	// SurrogateMax 是代理区终点。
	SurrogateMax Rune = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement Rune = 0xFFFD
	// BOM 是 U+FEFF。
	BOM Rune = 0xFEFF
)

// Valid 报告 r 是否为合法标量值：非负、不超 Max、不在代理区。
func Valid(r Rune) bool {
	return r >= 0 && r <= Max && (r < SurrogateMin || r > SurrogateMax)
}

// IsSurrogate 报告 r 是否落在 UTF-16 代理区。
func IsSurrogate(r Rune) bool {
	return r >= SurrogateMin && r <= SurrogateMax
}

// IsHighSurrogate 报告 r 是否为高代理代码单元（0xD800..0xDBFF）。
func IsHighSurrogate(r Rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为低代理代码单元（0xDC00..0xDFFF）。
func IsLowSurrogate(r Rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// JoinSurrogates 把一对代理还原成码点。调用方需保证二者分别为高/低代理。
func JoinSurrogates(hi, lo Rune) Rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// EncodeSurrogates 把 r（r >= 0x10000）编成高、低代理。
func EncodeSurrogates(r Rune) (hi, lo Rune) {
	v := r - 0x10000
	return 0xD800 + v>>10, 0xDC00 + v&0x3FF
}
