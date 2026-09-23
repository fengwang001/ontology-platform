// Package runes 按码点遍历 UTF-8 字节串。
// 非法字节按单个码点 U+FFFD 呈现，但通过 Raw 保留原字节参与字面比较。
package runes

// Rune 是一个遍历单位：合法 UTF-8 时 R 为码点、Width 为其字节长度；
// 非法字节时 R 为 unicode.ReplacementChar，Width 为 1，Raw 为那个原始字节。
type Rune struct {
	R     rune
	Width int
	Raw   byte
	Bad   bool
}

// Decode 解码 p[off:] 的第一个码点单位。off 必须落在合法边界上（由遍历保证）。
func Decode(p []byte, off int) Rune {
	r, w := utf8DecodeRune(p[off:])
	if r == replacementRune && w == 1 {
		return Rune{R: replacementRune, Width: 1, Raw: p[off], Bad: true}
	}
	return Rune{R: r, Width: w}
}

// Each 按码点单位调用 f，f 返回 false 时提前停止。
func Each(p []byte, f func(r Rune) bool) {
	for off := 0; off < len(p); {
		r := Decode(p, off)
		if !f(r) {
			return
		}
		off += r.Width
	}
}

// Count 返回字节串中的码点单位数（每个非法字节计 1）。
func Count(p []byte) int {
	n := 0
	Each(p, func(Rune) bool { n++; return true })
	return n
}

// Equal 判定两个码点单位在「字面」意义上是否相同：
// 合法码点比码点值；两个非法字节比原始字节；合法与非法永不相同。
func Equal(a, b Rune) bool {
	if a.Bad || b.Bad {
		return a.Bad && b.Bad && a.Raw == b.Raw
	}
	return a.R == b.R
}

// 不依赖 unicode/utf8 也能判定非法字节：直接实现，避免多余 import 形态差异。
const replacementRune = '\ufffd'

func utf8DecodeRune(p []byte) (r rune, size int) {
	if len(p) == 0 {
		return replacementRune, 0
	}
	c0 := p[0]
	switch {
	case c0 < 0x80:
		return rune(c0), 1
	case c0 < 0xC2:
		return replacementRune, 1
	case c0 < 0xE0:
		if len(p) < 2 || p[1]&0xC0 != 0x80 {
			return replacementRune, 1
		}
		return rune(c0&0x1F)<<6 | rune(p[1]&0x3F), 2
	case c0 < 0xF0:
		if len(p) < 3 || p[1]&0xC0 != 0x80 || p[2]&0xC0 != 0x80 {
			return replacementRune, 1
		}
		if c0 == 0xE0 && p[1] < 0xA0 {
			return replacementRune, 1
		}
		return rune(c0&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F), 3
	default:
		if len(p) < 4 || p[1]&0xC0 != 0x80 || p[2]&0xC0 != 0x80 || p[3]&0xC0 != 0x80 {
			return replacementRune, 1
		}
		if c0 == 0xF0 && p[1] < 0x90 || c0 > 0xF4 || c0 == 0xF4 && p[1] > 0x8F {
			return replacementRune, 1
		}
		return rune(c0&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F), 4
	}
}
