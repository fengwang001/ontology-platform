// Package scalar 判定单个 Unicode 标量值，不依赖其他包。
package scalar

const (
	MaxRune     = 0x10FFFF
	SurrogateLo = 0xD800
	SurrogateHi = 0xDFFF
)

// IsSurrogate 报告 r 是否落在代理区。
func IsSurrogate(r rune) bool {
	return uint32(r-SurrogateLo) <= SurrogateHi-SurrogateLo
}

// Valid 报告 r 是否为合法 Unicode 标量值（排除越界与代理区）。
func Valid(r rune) bool { return 0 <= r && r <= MaxRune && !IsSurrogate(r) }
