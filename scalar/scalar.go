// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

const (
	// MaxRune 是 Unicode 标量最大值。
	MaxRune = 0x10FFFF
	// SurrogateMin 高代理起点。
	SurrogateMin = 0xD800
	// SurrogateMax 低代理终点。
	SurrogateMax = 0xDFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是字节序标记 U+FEFF。
	BOM = 0xFEFF
)

// IsScalar 判定 r 是否为合法 Unicode 标量值（排除代理区与越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r < SurrogateMin || r > SurrogateMax && r <= MaxRune
}

// IsHighSurrogate 判定 r 是否为高代理代码单元。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 判定 r 是否为低代理代码单元。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// SurrogatePair 用高低代理组合标量；不合法时返回 false。
func SurrogatePair(hi, lo rune) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// Lead 描述一个 UTF-8 首字节的序列要求。
type Lead struct {
	Len        int  // 期望总长度（2..4）；非法首字节为 0
	SecondLo   byte // 第二字节合法下界（含）
	SecondHi   byte // 第二字节合法上界（含）
	ContinByte byte // 其余续字节是否要求 80..BF
}

// DecodeLead 判定 UTF-8 首字节，返回其序列约束。
// 非法首字节（C0/C1/F5..FF 与游离续字节）的 Len 为 0。
func DecodeLead(b byte) Lead {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return Lead{Len: 2, SecondLo: 0x80, SecondHi: 0xBF}
	case b == 0xE0:
		return Lead{Len: 3, SecondLo: 0xA0, SecondHi: 0xBF}
	case b >= 0xE1 && b <= 0xEC:
		return Lead{Len: 3, SecondLo: 0x80, SecondHi: 0xBF}
	case b == 0xED:
		return Lead{Len: 3, SecondLo: 0x80, SecondHi: 0x9F}
	case b >= 0xEE && b <= 0xEF:
		return Lead{Len: 3, SecondLo: 0x80, SecondHi: 0xBF}
	case b == 0xF0:
		return Lead{Len: 4, SecondLo: 0x90, SecondHi: 0xBF}
	case b >= 0xF1 && b <= 0xF3:
		return Lead{Len: 4, SecondLo: 0x80, SecondHi: 0xBF}
	case b == 0xF4:
		return Lead{Len: 4, SecondLo: 0x80, SecondHi: 0x8F}
	default:
		return Lead{}
	}
}

// IsCont 判定字节是否为 UTF-8 续字节。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }
