// Package esc 判定并组合 JSON 字符串里的单个转义序列。
// 它不依赖工程中的其他包。
package esc

// SimpleEscape 返回 \ 后单个字符对应的码点。
// ok 为 false 表示该字符不是 RFC 8259 允许的短转义。
func SimpleEscape(c byte) (r rune, ok bool) {
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
	default:
		return 0, false
	}
}

// HexVal 返回十六进制字符的值；非十六进制字符返回 false。
func HexVal(c byte) (v byte, ok bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// ParseHex4 解析恰好 4 个十六进制字符为一个码点。
// 调用方需保证 len(s) >= 4；任一字符非法时 ok 为 false。
func ParseHex4(s []byte) (r rune, ok bool) {
	for i := 0; i < 4; i++ {
		v, good := HexVal(s[i])
		if !good {
			return 0, false
		}
		r = r<<4 | rune(v)
	}
	return r, true
}

// IsHighSurrogate 报告码点是否落在 UTF-16 高代理区间 D800–DBFF。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告码点是否落在 UTF-16 低代理区间 DC00–DFFF。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Pair 组合一对高、低代理为一个码点。
// 调用方必须先确认两者分别是高、低代理。
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}
