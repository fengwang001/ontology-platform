// Package runes 按码点遍历 UTF-8 字节串。
//
// 非法字节按单字节码点 U+FFFD 处理；其原字节保留在 Raw 中参与逐字比较，
// 因此「合法编码的 U+FFFD」与「非法字节 0xFF」在逐字比较时互不相等。
package runes

import "unicode/utf8"

// RuneError 与 unicode/utf8 同义，非法字节解码为该码点。
const RuneError = utf8.RuneError

// Unit 是路径或模式中的一个码点单位。
type Unit struct {
	R   rune   // 解码后的码点；非法字节为 RuneError
	Raw string // 该码点在原串中占用的原始字节（1~4 字节）
}

// DecodeAt 返回 s[i:] 的第一个码点单位。i 必须满足 0 <= i <= len(s)。
func DecodeAt(s string, i int) Unit {
	r, size := utf8.DecodeRuneInString(s[i:])
	return Unit{R: r, Raw: s[i : i+size]}
}

// Split 把 s 切成码点单位序列；非法字节各占一个单位。
func Split(s string) []Unit {
	us := make([]Unit, 0, len(s))
	for i := 0; i < len(s); {
		u := DecodeAt(s, i)
		us = append(us, u)
		i += len(u.Raw)
	}
	return us
}

// Count 返回码点单位数（每个非法字节计 1）。
func Count(s string) int {
	n := 0
	for i := 0; i < len(s); n++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return n
}

// Equal 比较两个码点单位是否逐字节相同。
// 合法码点按码点值比较；RuneError 必须原始字节也相同才算相等。
func Equal(a, b Unit) bool {
	if a.R != RuneError {
		return a.R == b.R
	}
	return a.R == b.R && a.Raw == b.Raw
}
