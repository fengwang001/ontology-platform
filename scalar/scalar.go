// Package scalar 判定单个 Unicode 标量值。
package scalar

// Rune 是一个 Unicode 码点。
type Rune = rune

const (
	// MaxRune 是 Unicode 码点上限。
	MaxRune = 0x10FFFF
	// SurrogateMin 代理区下限。
	SurrogateMin = 0xD800
	// SurrogateMax 代理区上限。
	SurrogateMax = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是 U+FEFF。
	BOM = 0xFEFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、未越界、非负）。
func IsScalar(r Rune) bool {
	return r >= 0 && r < SurrogateMin || r > SurrogateMax && r <= MaxRune
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r Rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// FromSurrogatePair 用高低代理还原标量。
func FromSurrogatePair(hi, lo Rune) Rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// SurrogatePair 把 r（>=10000）编成高、低代理。
func SurrogatePair(r Rune) (hi, lo Rune) {
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF
}

// IsContinuation 报告 b 是否为 UTF-8 尾字节 10xxxxxx。
func IsContinuation(b byte) bool { return b&0xC0 == 0x80 }

// ExpectedLen 返回 UTF-8 首字节期望的序列长度；不可能的首字节返回 0。
func ExpectedLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

// SecondOK 报告首字节 lead 与第二字节 b 是否满足非最短/代理/越界约束。
func SecondOK(lead, b byte) bool {
	switch lead {
	case 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case 0xED:
		return b >= 0x80 && b <= 0x9F
	case 0xF0:
		return b >= 0x90 && b <= 0xBF
	case 0xF4:
		return b >= 0x80 && b <= 0x8F
	default:
		return IsContinuation(b)
	}
}
