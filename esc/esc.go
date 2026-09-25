// Package esc 判定 JSON 字符串内的单个转义序列。
// 它不依赖其他包，也不做 I/O。
package esc

// SimpleEscape 把 \X 中 X 对应的转义映射为其码点。
// 第二个返回值报告 X 是否是合法的单字符转义。
func SimpleEscape(x byte) (rune, bool) {
	switch x {
	case '"', '\\', '/':
		return rune(x), true
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
	default:
		return 0, false
	}
}

// HexDigit 把一个十六进制字符转成数值；非十六进制返回 false。
func HexDigit(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	default:
		return 0, false
	}
}

// IsHighSurrogate 报告 r 是否是 UTF-16 高代理码元。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否是 UTF-16 低代理码元。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Pair 组合一对高、低代理为码点。
// 调用方必须先用 IsHighSurrogate / IsLowSurrogate 校验。
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}
