// Package seg 实现字素簇的码点分类与「切/不切」边界判定，不依赖其他包。
package seg

import "unicode"

const (
	CR  = '\r'   // U+000D
	LF  = '\n'   // U+000A
	ZWJ = 0x200D // 零宽连接符
)

// Extend 报告 r 是否为粘合类码点：ZWJ、变体选择符、emoji 修饰符或组合字符。
func Extend(r rune) bool {
	switch {
	case r == ZWJ:
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // 变体选择符 1–16
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // 变体选择符补充
		return true
	case r >= 0x1F3FB && r <= 0x1F3FF: // emoji 肤色修饰符
		return true
	}
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

// Regional 报告 r 是否为区域指示符（U+1F1E6–U+1F1FF）。
func Regional(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

// NoBreak 报告相邻码点 a、b 之间是否「不切」。
// riRun 是到 a 为止（含 a）的连续 Regional 码点个数，仅规则 4 使用。
func NoBreak(a, b rune, riRun int) bool {
	switch {
	case a == CR && b == LF: // 规则 1：CRLF
		return true
	case a == ZWJ: // 规则 2：连接符粘合下一码点
		return true
	case Extend(b): // 规则 3：组合字符/变体/修饰符粘到前一码点
		return true
	case Regional(a) && Regional(b) && riRun%2 == 1: // 规则 4：RI 成对
		return true
	}
	return false
}
