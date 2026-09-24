// Package esc 识别 JSON 字符串中的单个转义序列，并负责 UTF-16 代理对的组合与校验。
package esc

// Simple 报告单字符转义 c（如 '"', '\\', '/', 'b', 'f', 'n', 'r', 't'）是否合法，
// 合法时返回其转义后的码点。
func Simple(c byte) (rune, bool) {
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

// Hex 解析一个十六进制数字；非十六进制字符时 ok 为 false。
func Hex(c byte) (v rune, ok bool) {
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

// HighSurrogate 报告 r 是否为 UTF-16 高代理码元（D800–DBFF）。
func HighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// LowSurrogate 报告 r 是否为 UTF-16 低代理码元（DC00–DFFF）。
func LowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Pair 将高代理 hi 与低代理 lo 组合成 Unicode 标量值。
// 二者不分别是高、低代理时 ok 为 false。
func Pair(hi, lo rune) (rune, bool) {
	if !HighSurrogate(hi) || !LowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// LowUnit 在 p 的起点解析一个 "\uXXXX" 低代理单元：要求恰好 6 字节、
// 四位均为十六进制，且结果落在低代理区间。任一条件不满足时 ok 为 false。
func LowUnit(p []byte) (rune, bool) {
	if len(p) < 6 || p[0] != '\\' || p[1] != 'u' {
		return 0, false
	}
	var r rune
	for i := 2; i < 6; i++ {
		v, ok := Hex(p[i])
		if !ok {
			return 0, false
		}
		r = r<<4 | v
	}
	return r, LowSurrogate(r)
}
