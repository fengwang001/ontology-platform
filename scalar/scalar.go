// Package scalar 判定单个 Unicode 标量值与 UTF 首字节类型。
// 不依赖工程内其他包，不使用 unicode/utf8、unicode/utf16。
package scalar

const (
	MaxRune   = 0x10FFFF // Unicode 码点上界
	Surrogate = 0xD800   // 代理区起点
	SurEnd    = 0xDFFF   // 代理区终点
	Replacement = 0xFFFD // 替换字符
)

// Valid 报告 r 是否为标量值：[0,0x10FFFF] 且不在代理区。
func Valid(r rune) bool { return r >= 0 && r <= MaxRune && !(r >= Surrogate && r <= SurEnd) }

// HighSurrogate 报告码元是否为高代理 D800..DBFF。
func HighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// LowSurrogate 报告码元是否为低代理 DC00..DFFF。
func LowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// Cont 报告字节是否为 UTF-8 续字节 10xxxxxx。
func Cont(b byte) bool { return b&0xC0 == 0x80 }

// U8Class 是 UTF-8 首字节分类。
type U8Class int

const (
	U8Bad       U8Class = iota // 80..BF / C0 C1 / F5..FF
	U8Len2                     // C2..DF
	U8E0                       // E0：第二字节须 A0..BF
	U8EMid                     // E1..EC、EE..EF：第二字节 80..BF
	U8ED                       // ED：第二字节须 80..9F
	U8F0                       // F0：第二字节须 90..BF
	U8FMid                     // F1..F3：第二字节 80..BF
	U8F4                       // F4：第二字节须 80..8F
)

// ClassifyU8 返回首字节分类与其声明的序列长度（非法首返回 1）。
func ClassifyU8(b byte) (U8Class, int) {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return U8Len2, 2
	case b == 0xE0:
		return U8E0, 3
	case (b >= 0xE1 && b <= 0xEC) || (b >= 0xEE && b <= 0xEF):
		return U8EMid, 3
	case b == 0xED:
		return U8ED, 3
	case b == 0xF0:
		return U8F0, 4
	case b >= 0xF1 && b <= 0xF3:
		return U8FMid, 4
	case b == 0xF4:
		return U8F4, 4
	default:
		return U8Bad, 1
	}
}

// SecondOK 报告给定首字节分类下第二字节是否落在合法区间。
func SecondOK(c U8Class, b byte) bool {
	switch c {
	case U8Len2, U8EMid, U8FMid:
		return Cont(b)
	case U8E0:
		return b >= 0xA0 && b <= 0xBF
	case U8ED:
		return b >= 0x80 && b <= 0x9F
	case U8F0:
		return b >= 0x90 && b <= 0xBF
	case U8F4:
		return b >= 0x80 && b <= 0x8F
	default:
		return false
	}
}
