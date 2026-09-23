package scalar

// 本包只做字节级判定，不依赖任何其他包，也不使用 unicode/utf8|utf16。

const (
	MaxRune   = 0x10FFFF
	Surrogate = 0xD800
)

func cont(b byte) bool { return b&0xC0 == 0x80 }

// LeadLen 返回 UTF-8 首字节声明的序列长度；非法首字节返回 0。
func LeadLen(b byte) int {
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

// SecondOK 判定首字节 lead 与第二字节 b2 是否构成合法前缀（同时排除
// 非最短形式、代理区与越界）。
func SecondOK(lead, b2 byte) bool {
	if !cont(b2) {
		return false
	}
	switch {
	case lead == 0xE0:
		return b2 >= 0xA0
	case lead == 0xED:
		return b2 <= 0x9F
	case lead == 0xF0:
		return b2 >= 0x90
	case lead == 0xF4:
		return b2 <= 0x8F
	default:
		return true
	}
}

func Valid(r rune) bool { return r >= 0 && r < Surrogate || r > 0xDFFF && r <= MaxRune }
func HighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }
func LowSurrogate(u uint16) bool  { return u >= 0xDC00 && u <= 0xDFFF }
func SurrogateCode(u uint16) bool { return u&0xF800 == 0xD800 }

// Pair 合并高/低代理，调用方需保证二者均为合法代理。
func Pair(hi, lo uint16) rune {
	return rune(uint32(hi-0xD800)<<10|uint32(lo-0xDC00)) + 0x10000
}

// SplitPair 返回 r（r>=0x10000）的高、低代理。
func SplitPair(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}
