package editdist

import (
	"unicode"
	"unicode/utf8"
)

// decodeRunes 把 s 严格解码为码点序列。
// 遇到非法 UTF-8 字节序列时返回 *UTF8Error，指出侧别与字节偏移，
// 绝不按 utf8.RuneError 静默替换（合法的 U+FFFD 编码长度为 3，不受影响）。
func decodeRunes(s, side string) ([]rune, error) {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &UTF8Error{Side: side, Offset: i}
		}
		rs = append(rs, r)
		i += size
	}
	return rs, nil
}

// RuneLen 返回 s 的码点（rune）数量。
// 非法 UTF-8 返回 *UTF8Error（Side 为 "input"），可用于测试核对码点语义。
func RuneLen(s string) (int, error) {
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return 0, &UTF8Error{Side: "input", Offset: i}
		}
		n++
		i += size
	}
	return n, nil
}

// foldRune 返回 r 在 Unicode 简单大小写折叠（simple case folding）
// 等价类中的最小代表元。这是逐码点折叠：能处理 'K'/'K'/'k'、
// 'ß'/'ẞ' 这类单码点等价，但不做 ß→ss 之类的全折叠（full folding，
// 即一个码点展开为多个码点）。"Straße" 与 "STRASSE" 因此仍不相等。
func foldRune(r rune) rune {
	min := r
	for s := unicode.SimpleFold(r); s != r; s = unicode.SimpleFold(s) {
		if s < min {
			min = s
		}
	}
	return min
}

// foldRunes 原地对码点序列做简单大小写折叠。
func foldRunes(rs []rune) {
	for i, r := range rs {
		rs[i] = foldRune(r)
	}
}
