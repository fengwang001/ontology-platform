// Package esc 判定单个 JSON 字符串转义序列，并组合 UTF-16 代理对。
// 它不依赖其他包，也不关心引号与整体字面量边界。
package esc

const (
	flagSimple = 1 << iota // \" \\ \/ \b \f \n \r \t
	flagU                  // \uXXXX
)

var table [256]uint8

func init() {
	for _, c := range `"\/bfnrt` {
		table[c] = flagSimple
	}
	table['u'] = flagU
}

// IsEscapable 报告反斜杠后的字符是否构成合法简单转义（不含 u）。
func IsEscapable(c byte) bool { return table[c]&flagSimple != 0 }

// IsUnicodeStart 报告反斜杠后的字符是否为 u（开始 \uXXXX）。
func IsUnicodeStart(c byte) bool { return table[c]&flagU != 0 }

// HexValue 返回十六进制数字的数值；非十六进制字节返回 false。
func HexValue(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// DecodeHex4 解码恰好 4 个十六进制字节；任何一位非法时返回 false。
func DecodeHex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var v rune
	for _, c := range b[:4] {
		h, ok := HexValue(c)
		if !ok {
			return 0, false
		}
		v = v<<4 | rune(h)
	}
	return v, true
}

// SimpleRune 返回简单转义字符所代表的码点。
func SimpleRune(c byte) (rune, bool) {
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

// IsHighSurrogate 报告码元是否为 UTF-16 高代理 U+D800–U+DBFF。
func IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLowSurrogate 报告码元是否为 UTF-16 低代理 U+DC00–U+DFFF。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Pair 把高低代理组合为单个码点；入参不在代理区间时返回 false。
func Pair(hi, lo rune) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}
