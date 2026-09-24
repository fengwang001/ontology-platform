// Package esc 判定 JSON 字符串中的单个转义序列，并组合 UTF-16 代理对。
// 它不依赖工程内其他包，也不使用 encoding/json、strconv、unicode/utf16。
package esc

// IsHexDigit 报告 b 是否为一个十六进制数字字节。
func IsHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// HexValue 返回十六进制数字字节的值；对非十六进制字节返回 (0,false)。
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

// IsSimpleEscape 报告 \" \\ \/ \b \f \n \r \t 这八种短转义中，
// 反斜杠后的字符是否合法。
func IsSimpleEscape(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	}
	return false
}

// UnescapeSimple 返回短转义对应的实际字节；对非法字符返回 0。
func UnescapeSimple(c byte) byte {
	switch c {
	case '"':
		return '"'
	case '\\':
		return '\\'
	case '/':
		return '/'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	}
	return 0
}

// DecodeU4 把恰好四个十六进制数字解析为 16 位无符号值。
func DecodeU4(p []byte) (uint16, bool) {
	var v uint16
	if len(p) != 4 {
		return 0, false
	}
	for i := 0; i < 4; i++ {
		d, ok := HexValue(p[i])
		if !ok {
			return 0, false
		}
		v = v<<4 | uint16(d)
	}
	return v, true
}

// IsHighSurrogate 报告 v 是否为 UTF-16 高代理码元 U+D800–U+DBFF。
func IsHighSurrogate(v uint16) bool { return v >= 0xD800 && v <= 0xDBFF }

// IsLowSurrogate 报告 v 是否为 UTF-16 低代理码元 U+DC00–U+DFFF。
func IsLowSurrogate(v uint16) bool { return v >= 0xDC00 && v <= 0xDFFF }

// CombineSurrogates 按 RFC 2781 公式组合一对代理为码点。
func CombineSurrogates(hi, lo uint16) rune {
	return rune(hi-0xD800)<<10 | rune(lo-0xDC00) + 0x10000
}

// EncodeHex4 把 v 的低 16 位写为四个小写十六进制数字。
func EncodeHex4(v uint16) [4]byte {
	const hex = "0123456789abcdef"
	return [4]byte{hex[v>>12&0xF], hex[v>>8&0xF], hex[v>>4&0xF], hex[v&0xF]}
}

