// Package scalar 只做单个 Unicode 标量值与 UTF-16 代理码元的判定，
// 不依赖本工程任何其他包。
package scalar

// Value 是一个 Unicode 码点（可能未通过合法性校验）。
type Value = rune

const (
	// Max 是 Unicode 码点上限。
	Max Value = 0x10FFFF
	// SurrogateLo / SurrogateHi 夹住代理区 [D800, DFFF]。
	SurrogateLo Value = 0xD800
	SurrogateHi Value = 0xDFFF
	// HighLo/HighHi 为高代理区，LowLo/LowHi 为低代理区。
	HighLo Value = 0xD800
	HighHi Value = 0xDBFF
	LowLo  Value = 0xDC00
	LowHi  Value = 0xDFFF
	// Replacement 是 U+FFFD。
	Replacement Value = 0xFFFD
	// BOM 是 U+FEFF。
	BOM Value = 0xFEFF
)

// Valid 判断 r 是否为合法 Unicode 标量值：非负、不越界、不在代理区。
func Valid(r Value) bool {
	return r >= 0 && r <= Max && !IsSurrogate(r)
}

// IsSurrogate 判断 r 是否落在代理区。
func IsSurrogate(r Value) bool { return r >= SurrogateLo && r <= SurrogateHi }

// IsHighSurrogate 判断码元 u 是否为高代理（前导代理）。
func IsHighSurrogate(u uint16) bool { return u >= HighLo && u <= HighHi }

// IsLowSurrogate 判断码元 u 是否为低代理（尾随代理）。
func IsLowSurrogate(u uint16) bool { return u >= LowLo && u <= LowHi }

// DecodeSurrogate 将合法高/低代理对还原为标量值；
// 入参不是一对合法代理时第二个返回值为 false。
func DecodeSurrogate(hi, lo uint16) (Value, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return Value(uint32(hi-HighLo)<<10 | uint32(lo-LowLo)) + 0x10000, true
}

// EncodeSurrogate 把 r（必须 ≥ 0x10000 且 ≤ Max）编为高/低代理；
// r 超出辅助平面时第二个返回值为 false。
func EncodeSurrogate(r Value) (uint16, uint16, bool) {
	if r < 0x10000 || r > Max {
		return 0, 0, false
	}
	v := uint32(r) - 0x10000
	return uint16(v>>10) + uint16(HighLo), uint16(v&0x3FF) + uint16(LowLo), true
}
