// Package esc 判定 JSON 字符串中的单个转义序列（RFC 8259），并负责
// UTF-16 代理对的组合与校验。它不依赖工程内其他包。
package esc

import "fmt"

// ErrEscape 描述一个非法转义序列；Offset 由调用方（jstr）按字节流位置填写。
type ErrEscape struct {
	Offset int
	Reason Reason
}

// Reason 区分非法转义的原因，使错误彼此可判定。
type Reason int

const (
	// RUnknown：\ 后不是合法转义字符（如 \x \' \U）。
	RUnknown Reason = iota + 1
	// RShortHex：\u 后不足 4 位十六进制。
	RShortHex
	// RLoneHigh：高代理后缺少低代理。
	RLoneHigh
	// RLoneLow：低代理没有前置高代理。
	RLoneLow
	// RBadLow：高代理后紧跟的不是低代理。
	RBadLow
)

func (e *ErrEscape) Error() string {
	return fmt.Sprintf("esc: invalid escape at byte %d: %s", e.Offset, e.Reason)
}

func (r Reason) String() string {
	switch r {
	case RUnknown:
		return "unknown escape character"
	case RShortHex:
		return `\u requires 4 hex digits`
	case RLoneHigh:
		return "lone high surrogate"
	case RLoneLow:
		return "lone low surrogate"
	case RBadLow:
		return "high surrogate not followed by low surrogate"
	default:
		return "unknown reason"
	}
}

// Simple 解码反斜杠后的单字符短转义（不含 \uXXXX）。
// 第二个返回值报告该字符是否为合法短转义。
func Simple(c byte) (rune, bool) {
	switch c {
	case '"', '/', '\\':
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

// HexDigit 返回一位十六进制数的值。
func HexDigit(c byte) (int, bool) {
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

// Hex4 解码恰好 4 位十六进制数；非法或长度不足时 ok 为 false。
func Hex4(h []byte) (rune, bool) {
	if len(h) != 4 {
		return 0, false
	}
	v := 0
	for _, c := range h {
		d, ok := HexDigit(c)
		if !ok {
			return 0, false
		}
		v = v<<4 | d
	}
	return rune(v), true
}

// IsHigh 报告 r 是否为 UTF-16 高代理码元（D800–DBFF）。
func IsHigh(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// IsLow 报告 r 是否为 UTF-16 低代理码元（DC00–DFFF）。
func IsLow(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }

// Pair 把一对高/低代理码元组合成标量值；非法组合时 ok 为 false。
func Pair(hi, lo rune) (rune, bool) {
	if !IsHigh(hi) || !IsLow(lo) {
		return 0, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}
