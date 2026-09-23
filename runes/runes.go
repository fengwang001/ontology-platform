// Package runes 按码点遍历 UTF-8 字节串。
// 非法字节按单字节码点 U+FFFD 处理，但比较时保留原字节语义：
// 不同的非法字节互不相等，非法字节也不等于合法的 U+FFFD 编码。
package runes

import "unicode/utf8"

// RuneError 即 utf8.RuneError（U+FFFD）。
const RuneError = utf8.RuneError

// Token 是一个码点的解码结果。Raw 保留原始字节。
type Token struct {
	Rune rune // 解码出的码点；非法字节为 U+FFFD
	Size int  // 占用的字节数；非法字节为 1
	Raw  byte // 首字节；用于区分互不相同的非法字节
}

// Decode 返回 b 中字节偏移 i 处的码点。
// 调用方需保证 i < len(b)。
func Decode(b []byte, i int) Token {
	r, size := utf8.DecodeRune(b[i:])
	if r == utf8.RuneError && size == 1 && b[i] >= utf8.RuneSelf {
		// 非法字节：按单字节码点 U+FFFD 处理，但用 Raw 保留原字节。
		return Token{Rune: utf8.RuneError, Size: 1, Raw: b[i]}
	}
	return Token{Rune: r, Size: size, Raw: b[i]}
}

// Equal 判断两个码点是否相等：码点相同，且非法字节时原字节也相同。
func Equal(a, b Token) bool {
	if a.Rune != b.Rune {
		return false
	}
	if a.Rune == utf8.RuneError && a.Size == 1 && a.Raw >= utf8.RuneSelf {
		return a.Raw == b.Raw && b.Size == 1
	}
	return true
}

// Len 返回 b 的码点数（非法字节按 1 个码点计）。
func Len(b []byte) int {
	n := 0
	for i := 0; i < len(b); {
		n++
		i += Decode(b, i).Size
	}
	return n
}
