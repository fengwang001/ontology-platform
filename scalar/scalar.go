// Package scalar 判定单个 Unicode 标量值的合法性。
package scalar

// Rune 是一个 Unicode 码点值。
type Rune = int32

// Max 是 Unicode 码点上界。
const Max = 0x10FFFF

// Valid 报告 r 是否为合法 Unicode 标量值（非代理、非越界、非负）。
func Valid(r Rune) bool { return r >= 0 && r <= Max && !Surrogate(r) }

// HighSurrogate 报告码元是否为高代理 D800..DBFF。
func HighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// LowSurrogate 报告码元是否为低代理 DC00..DFFF。
func LowSurrogate(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// Surrogate 报告码点是否落在代理区 D800..DFFF。
func Surrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// Pair 把高、低代理码元组合成标量；调用方需先确认两者身份。
func Pair(hi, lo uint16) Rune {
	return 0x10000 + (Rune(hi)-0xD800)<<10 + (Rune(lo) - 0xDC00)
}
