// Package scalar 判定单个 Unicode 标量值与 UTF 字节级属性。不依赖其他包。
package scalar

const (
	// RuneSelf 单字节 ASCII 上界。
	RuneSelf = 0x80
	// MaxRune Unicode 标量值上界 U+10FFFF。
	MaxRune = '\U0010FFFF'
	// Replacement 替换字符 U+FFFD。
	Replacement = '\uFFFD'
	// BOM 字节序标记 U+FEFF。
	BOM = '\uFEFF'
	// SurrogateMin 高/低代理区起点 U+D800。
	SurrogateMin = 0xD800
	// SurrogateMax 代理区终点 U+DFFF。
	SurrogateMax = 0xDFFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、未越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateMin && r <= SurrogateMax)
}

// IsContinuation 报告 b 是否为续写字节 10xxxxxx。
func IsContinuation(b byte) bool { return b&0xC0 == 0x80 }

// SeqLen 返回首字节声明的序列总长度；非法首字节返回 0；ASCII 返回 1。
func SeqLen(b0 byte) int {
	switch {
	case b0 < RuneSelf:
		return 1
	case b0 >= 0xC2 && b0 <= 0xDF:
		return 2
	case b0 >= 0xE0 && b0 <= 0xEF:
		return 3
	case b0 >= 0xF0 && b0 <= 0xF4:
		return 4
	default:
		return 0
	}
}

// SecondOK 报告首字节 b0 的第二字节 b1 是否落在其合法区间。
// E0 要求 A0..BF（拒非最短），ED 要求 80..9F（拒代理），
// F0 要求 90..BF，F4 要求 80..8F（拒越界），其余为 80..BF。
func SecondOK(b0, b1 byte) bool {
	if !IsContinuation(b1) {
		return false
	}
	switch b0 {
	case 0xE0:
		return b1 >= 0xA0
	case 0xED:
		return b1 < 0xA0
	case 0xF0:
		return b1 >= 0x90
	case 0xF4:
		return b1 <= 0x8F
	default:
		return true
	}
}

// IsHighSurrogate 报告 code unit 是否为高代理。
func IsHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLowSurrogate 报告 code unit 是否为低代理。
func IsLowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// SurrogatePair 组合高/低代理为标量值（调用方需先验证代理性质）。
func SurrogatePair(hi, lo uint16) rune {
	return rune(uint32(hi-0xD800)<<10|uint32(lo-0xDC00)) + 0x10000
}

// EncodeSurrogate 把 r（必须 ≥U+10000）编码为高、低代理。
func EncodeSurrogate(r rune) (uint16, uint16) {
	v := uint32(r) - 0x10000
	return uint16(v>>10) + 0xD800, uint16(v&0x3FF) + 0xDC00
}
