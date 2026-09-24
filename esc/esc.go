// Package esc 判定单个 JSON 字符串转义序列，并组合/校验 UTF-16 代理对。
// 它不依赖其他包，也不读写引号或字面量结构。
package esc

const (
	// SurrogateMin 是 UTF-16 高/低代理码点区间起点 0xD800。
	SurrogateMin = 0xD800
	// SurrogateMax 是代理码点区间终点 0xDFFF。
	SurrogateMax = 0xDFFF
)

// IsHex 报告 b 是否为一个十六进制数字（大小写均可）。
func IsHex(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'f':
		return true
	case b >= 'A' && b <= 'F':
		return true
	}
	return false
}

// HexValue 返回十六进制数字 b 的数值；非十六进制时返回 (0,false)。
func HexValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// IsSimpleEscape 报告 '\' 之后的字节是否是八个短形式转义之一：
// " \ / b f n r t。
func IsSimpleEscape(b byte) bool {
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}

// SimpleUnescape 返回短形式转义 c 对应的实际字节；未知时返回 (0,false)。
func SimpleUnescape(c byte) (byte, bool) {
	switch c {
	case '"':
		return '"', true
	case '\\':
		return '\\', true
	case '/':
		return '/', true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// IsHighSurrogate 报告 r 是否为高（先导）代理 0xD800–0xDBFF。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低（尾随）代理 0xDC00–0xDFFF。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// IsSurrogate 报告 r 是否落在任意一个代理区间 0xD800–0xDFFF。
func IsSurrogate(r rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// CombineSurrogate 将高代理 high 与低代理 low 组合为完整码点。
// 任一参数不是对应区间的代理时返回 (RuneError,false)。
func CombineSurrogate(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return RuneError, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}

// RuneError 表示代理组合失败时返回的非法码点（U+FFFD 仅作哨兵，调用方不得写出它）。
const RuneError rune = 0xFFFD
