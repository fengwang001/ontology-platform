// Package esc 判定 JSON 字符串中的单个转义序列（RFC 8259）。
//
// 支持的转义：\" \\ \/ \b \f \n \r \t 以及 \uXXXX；
// 并负责 UTF-16 代理对的组合与校验。本包不依赖其他包。
package esc

const hexLen = 4

// IsHexDigit 报告 c 是否为十六进制数字。
func IsHexDigit(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// HexValue 返回十六进制数字的值；非十六进制时返回 (0, false)。
func HexValue(c byte) (rune, bool) {
	switch {
	case '0' <= c && c <= '9':
		return rune(c - '0'), true
	case 'a' <= c && c <= 'f':
		return rune(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return rune(c-'A') + 10, true
	}
	return 0, false
}

// DecodeHex 将恰好 4 个十六进制字节解码为一个 16 位值。
// 任一字符非法时返回 (0, false)。
func DecodeHex(b []byte) (rune, bool) {
	if len(b) != hexLen {
		return 0, false
	}
	var v rune
	for _, c := range b {
		d, ok := HexValue(c)
		if !ok {
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// SimpleEscape 返回反斜杠之后单个字符所表示的码点。
// 未知转义时返回 (0, false)。
func SimpleEscape(c byte) (rune, bool) {
	switch c {
	case '"', '\\', '/':
		return rune(c), true
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

// IsHighSurrogate 报告 v 是否为 UTF-16 高代理（D800–DBFF）。
func IsHighSurrogate(v rune) bool { return 0xD800 <= v && v <= 0xDBFF }

// IsLowSurrogate 报告 v 是否为 UTF-16 低代理（DC00–DFFF）。
func IsLowSurrogate(v rune) bool { return 0xDC00 <= v && v <= 0xDFFF }

// SurrogatePair 组合匹配的高、低代理为一个 Unicode 码点；
// 输入不构成代理对时返回 (0, false)。
func SurrogatePair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return 0, false
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00), true
}
