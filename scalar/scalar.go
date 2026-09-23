package scalar

// Rune 是一个待判定的 32 位码点。
type Rune = uint32

// IsScalar 判定 r 是否为 Unicode 标量值：非代理区且不越界。
func IsScalar(r Rune) bool {
	return r <= 0x10FFFF && !(r >= 0xD800 && r <= 0xDFFF)
}

// IsSurrogate 判定 r 是否落在高/低代理区。
func IsSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// IsHighSurrogate / IsLowSurrogate 判定代理半区。
func IsHighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func IsLowSurrogate(r Rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }

// CombineSurrogates 由高、低代理还原辅助平面码点（调用方须自行保证范围）。
func CombineSurrogates(hi, lo Rune) Rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// RuneLen 返回标量的 UTF-8 编码字节数；越界/代理返回 0。
func RuneLen(r Rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case IsSurrogate(r) || r > 0x10FFFF:
		return 0
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
