// Package scalar 判断单个 Unicode 标量值是否合法。
package scalar

// Rune 是一个 21 位码点的承载类型。
type Rune = rune

// Valid 报告 r 是否为合法 Unicode 标量值（非代理、未越界）。
func Valid(r Rune) bool {
	return r >= 0 && r <= 0x10FFFF && !Surrogate(r)
}

// Surrogate 报告 r 是否落在 UTF-16 代理区 D800..DFFF。
func Surrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// HighSurrogate 报告 r 是否为高代理 D800..DBFF。
func HighSurrogate(r Rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// LowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func LowSurrogate(r Rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

const (
	MaxRune     Rune = 0x10FFFF
	Replacement Rune = 0xFFFD
	BOM         Rune = 0xFEFF
)
