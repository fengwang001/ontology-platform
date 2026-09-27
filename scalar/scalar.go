// Package scalar 判定单个 Unicode 标量值与 UTF-8 字节类。
package scalar

// Rune 是一个 Unicode 码点（未判定是否标量）。
type Rune = rune

const (
	MaxRune   = 0x10FFFF
	Surrogate = 0xFFFD // 替换字符
	HighLo    = 0xD800
	HighHi    = 0xDBFF
	LowLo     = 0xDC00
	LowHi     = 0xDFFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界）。
func IsScalar(r Rune) bool {
	return uint32(r) <= MaxRune && !(HighLo <= r && r <= LowHi)
}

// IsHighSurrogate 报告 r 是否为高代理项。
func IsHighSurrogate(r Rune) bool { return HighLo <= r && r <= HighHi }

// IsLowSurrogate 报告 r 是否为低代理项。
func IsLowSurrogate(r Rune) bool { return LowLo <= r && r <= LowHi }

// SeqLen 返回 UTF-8 首字节 b 声明的序列长度（1..4）；非法首字节返回 0。
func SeqLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case 0xC2 <= b && b <= 0xDF:
		return 2
	case 0xE0 <= b && b <= 0xEF:
		return 3
	case 0xF0 <= b && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

// SecondOK 报告首字节 lead 与其第二字节 second 是否构成合法的最短形式前缀。
// 仅在 SeqLen(lead)>=2 时有意义。
func SecondOK(lead, second byte) bool {
	cont := 0x80 <= second && second <= 0xBF
	switch lead {
	case 0xE0:
		return cont && 0xA0 <= second
	case 0xED:
		return cont && second <= 0x9F
	case 0xF0:
		return cont && 0x90 <= second
	case 0xF4:
		return cont && second <= 0x8F
	default:
		return cont
	}
}

// IsCont 报告 b 是否为 UTF-8 续字节。
func IsCont(b byte) bool { return 0x80 <= b && b <= 0xBF }
