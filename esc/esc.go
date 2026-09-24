// Package esc 判定 JSON 字符串中的单个转义序列，并完成 UTF-16 代理对的组合与校验。
// 本包不依赖任何其他包，只做纯函数式的查表与计算。
package esc

// Simple 返回单字符转义（\" \\ \/ \b \f \n \r \t）解码后的字节。
// c 是反斜杠后的那个字符；不是合法单字符转义时 ok 为 false。
func Simple(c byte) (decoded byte, ok bool) {
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

// Hex 返回单个十六进制数字（大小写均可）的值；非十六进制字符时 ok 为 false。
func Hex(c byte) (v uint16, ok bool) {
	switch {
	case '0' <= c && c <= '9':
		return uint16(c - '0'), true
	case 'a' <= c && c <= 'f':
		return uint16(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return uint16(c-'A') + 10, true
	}
	return 0, false
}

// IsHigh 报告 u 是否为 UTF-16 高代理码元（U+D800–U+DBFF）。
func IsHigh(u uint16) bool { return 0xD800 <= u && u <= 0xDBFF }

// IsLow 报告 u 是否为 UTF-16 低代理码元（U+DC00–U+DFFF）。
func IsLow(u uint16) bool { return 0xDC00 <= u && u <= 0xDFFF }

// Combine 把一对合法代理码元组合成增补平面码点（U+10000–U+10FFFF）。
// 调用方必须保证 IsHigh(hi) 且 IsLow(lo)。
func Combine(hi, lo uint16) rune {
	return 0x10000 + (rune(hi-0xD800) << 10) + rune(lo-0xDC00)
}
