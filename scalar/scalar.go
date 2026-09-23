// Package scalar 只负责单个 Unicode 标量值与 UTF 首/续字节的判定。
// 不依赖本工程其他包，也不使用 unicode/utf8、unicode/utf16。
package scalar

// Rune 是一个 Unicode 码位（不一定是标量值）。
type Rune = rune

const (
	// MaxRune 是 Unicode 码位上界。
	MaxRune = 0x10FFFF
	// SurrogateMin/Max 是代理区。
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement = '\uFFFD'
	// BOM 是 U+FEFF。
	BOM = '\uFEFF'
)

// IsScalar 判定 r 是否为合法 Unicode 标量值（非代理、不越界）。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateMin && r <= SurrogateMax)
}

// IsSurrogate 判定 r 是否落在高/低代理区。
func IsSurrogate(r Rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate 判定 r 是否为高代理。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 判定 r 是否为低代理。
func IsLowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// IsCont8 判定 b 是否为 UTF-8 续字节 10xxxxxx。
func IsCont8(b byte) bool { return b&0xC0 == 0x80 }

// LeadLen 返回 UTF-8 首字节声称的序列长度；非法首字节返回 0。
func LeadLen(b byte) int {
	switch {
	case b <= 0x7F:
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

// SecondOK 判定 UTF-8 多字节序列首字节 lead 与第二字节 b 是否构成合法前缀。
// 编码了 E0/ED/F0/F4 的特殊第二字节区间，用于拒绝非最短形式、代理区与越界。
func SecondOK(lead, b byte) bool {
	if !IsCont8(b) {
		return false
	}
	switch lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

// DecodeSurrogate 把高/低代理对组合成码位；入参必须分别为高、低代理。
func DecodeSurrogate(hi, lo Rune) Rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// EncodeSurrogate 把 r（r >= 0x10000）拆成高、低代理。
func EncodeSurrogate(r Rune) (hi, lo Rune) {
	r -= 0x10000
	return 0xD800 + (r >> 10), 0xDC00 + (r & 0x3FF)
}
