// Package scalar 判定单个 Unicode 标量值（不依赖其他包）。
package scalar

const (
	// MaxRune 是 Unicode 最大码位。
	MaxRune = 0x10FFFF
	// RuneError 是替换字符 U+FFFD。
	RuneError rune = '\uFFFD'
	// MaxUTF8 是单个标量的最大 UTF-8 字节数。
	MaxUTF8 = 4
)

// Surrogate 报告 r 是否落在 UTF-16 代理区。
func Surrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// Valid 报告 r 是否为合法 Unicode 标量值。
func Valid(r rune) bool {
	return r >= 0 && r <= MaxRune && !Surrogate(r)
}
