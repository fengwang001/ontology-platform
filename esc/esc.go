// Package esc 判定并组合 JSON 字符串内的单个转义序列。
package esc

// IsHex 报告 b 是否为十六进制数字。
func IsHex(b byte) bool {
	return HexVal(b) >= 0
}

// HexVal 返回十六进制数字的值，非十六进制返回 -1。
func HexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}

// IsSimpleEscape 报告 b 是否为 \" \\ \/ \b \f \n \r \t 中的简单转义后随字符。
func IsSimpleEscape(b byte) bool {
	return SimpleRune(b) != RuneError
}

// SimpleRune 返回简单转义后随字符对应的码点，非法返回 RuneError。
func SimpleRune(b byte) rune {
	switch b {
	case '"', '\\', '/':
		return rune(b)
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
	default:
		return RuneError
	}
}

// RuneError 是本包使用的"非法码点"标记。
const RuneError rune = -1

// IsHighSurrogate 报告 r 是否为 UTF-16 高代理码元。
func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为 UTF-16 低代理码元。
func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// SurrogatePair 将合法的高/低代理码元组合成一个码点；输入非法返回 RuneError。
func SurrogatePair(high, low rune) rune {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return RuneError
	}
	return 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
}
