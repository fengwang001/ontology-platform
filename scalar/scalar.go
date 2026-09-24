// Package scalar 判定单个 Unicode 标量值的合法区间。
// 本包不依赖工程内其他包，所有判定均为手工位运算。
package scalar

// RuneError 是替换字符 U+FFFD。
const RuneError rune = 0xFFFD

// MaxRune 是 Unicode 标量值上界 U+10FFFF。
const MaxRune rune = 0x10FFFF

// SurrogateMin/SurrogateMax 圈定代理区 U+D800..U+DFFF。
const (
	SurrogateMin rune = 0xD800
	SurrogateMax rune = 0xDFFF
	BOM          rune = 0xFEFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值：
// 非负、不超过 U+10FFFF、且不落在代理区。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool {
	return r >= SurrogateMin && r <= SurrogateMax
}

// IsHighSurrogate 报告 u 是否为高代理 U+D800..U+DBFF。
func IsHighSurrogate(u uint16) bool {
	return u >= 0xD800 && u <= 0xDBFF
}

// IsLowSurrogate 报告 u 是否为低代理 U+DC00..U+DFFF。
func IsLowSurrogate(u uint16) bool {
	return u >= 0xDC00 && u <= 0xDFFF
}

// SurrogatePair 把高代理 hi、低代理 lo 组合成标量值。
func SurrogatePair(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-0xD800) << 10) + rune(lo-0xDC00)
}

// EncodeSurrogatePair 把 U+10000 以上的标量编码成高、低代理。
func EncodeSurrogatePair(r rune) (hi, lo uint16) {
	v := uint32(r) - 0x10000
	return uint16(0xD800 + v>>10), uint16(0xDC00 + v&0x3FF)
}

// Continuation 报告 b 是否为 UTF-8 续字节 10xxxxxx。
func Continuation(b byte) bool { return b&0xC0 == 0x80 }

// InRange 报告开区间判定用的简单辅助：lo <= r && r <= hi。
func InRange(r, lo, hi rune) bool { return r >= lo && r <= hi }
