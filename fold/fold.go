// Package fold 实现头部值的折行展开与重新折行。
//
// 展开规则：续行（以 SP/HTAB 开头的行）前导空白压成一个空格并入上一值。
// 重折规则：只允许在值中已有的、且后随非空格字符的空格处，
// 把该空格替换为 CRLF+SP，因此展开后能精确还原（往返无损）。
package fold

import "strings"

// IsContinuation 报告一行是否为续行（以 SP 或 HTAB 开头）。
func IsContinuation(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// Unfold 把首行值 first 与后续续行 conts 合并为一个值：
// 每个续行的前导空白被压成恰好一个空格。行尾空白由调用方统一 trim。
func Unfold(first string, conts []string) string {
	if len(conts) == 0 {
		return first
	}
	var b strings.Builder
	b.WriteString(first)
	for _, c := range conts {
		b.WriteByte(' ')
		b.WriteString(strings.TrimLeft(c, " \t"))
	}
	return b.String()
}

// Fold 把 "name: value" 按宽度 width 折成若干行（不含行尾 CRLF）。
// width <= 0 或整行不超宽时返回单行。折点只取值中"后随非空格"的空格，
// 保证再展开后值逐字节不变；若某段无可折空格，该行允许超宽（无损优先）。
func Fold(name, value string, width int) []string {
	head := name + ": "
	if width <= 0 || len(head)+len(value) <= width {
		return []string{head + value}
	}
	lines := make([]string, 0, 4)
	cur := head
	breakAt := -1 // cur 中来自 value 的可折空格位置
	for i := 0; i < len(value); i++ {
		cur += value[i : i+1]
		if value[i] == ' ' && i+1 < len(value) && value[i+1] != ' ' {
			breakAt = len(cur) - 1
		}
		if len(cur) > width && breakAt > 0 {
			lines = append(lines, cur[:breakAt])
			cur = " " + cur[breakAt+1:]
			breakAt = -1
		}
	}
	return append(lines, cur)
}
