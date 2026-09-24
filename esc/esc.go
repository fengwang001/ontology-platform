// Package esc 判定与解码 JSON 字符串中的单个转义序列，
// 并负责 UTF-16 代理对的组合与校验。它不依赖其他包。
package esc

// Simple 解码 "\""、"\\"、"\/"、"\b"、"\f"、"\n"、"\r"、"\t"。
// c 为反斜杠后的那个字节；ok 为 false 表示未知简单转义。
func Simple(c byte) (r rune, ok bool) {
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
	default:
		return 0, false
	}
}

// HexValue 返回单个十六进制字符的数值；非十六进制时 ok 为 false。
func HexValue(c byte) (v int, ok bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	default:
		return 0, false
	}
}

// Hex4 解码恰好 4 个十六进制字符为一个 16 位值；任一字符非法时 ok 为 false。
func Hex4(b []byte) (r rune, ok bool) {
	if len(b) != 4 {
		return 0, false
	}
	var v rune
	for _, c := range b {
		d, good := HexValue(c)
		if !good {
			return 0, false
		}
		v = v<<4 | rune(d)
	}
	return v, true
}

// IsHigh 报告 r 是否为 UTF-16 高代理（前导代理）。
func IsHigh(r rune) bool { return 0xD800 <= r && r <= 0xDBFF }

// IsLow 报告 r 是否为 UTF-16 低代理（尾随代理）。
func IsLow(r rune) bool { return 0xDC00 <= r && r <= 0xDFFF }

// Pair 组合一对已确认的高、低代理为一个标量码点。
// 调用方必须先用 IsHigh/IsLow 校验，否则结果无意义。
func Pair(high, low rune) rune {
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}
