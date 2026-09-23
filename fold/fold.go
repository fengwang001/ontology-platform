// Package fold 实现折行续行的展开与重新折行。
//
// 展开：以空白开头的行并入上一行，前导空白压成一个空格；
// 首行就是续行是语法错误。重折：超过宽度的值在空格处断开，
// 续行以单个空格开头，保证展开后值完全还原（往返无损）。
package fold

import (
	"errors"
	"strings"
)

// ErrLeadingContinuation 表示首行就是续行。
var ErrLeadingContinuation = errors.New("fold: first line is a continuation")

// IsContinuation 报告一行是否以空白（SP/HTAB）开头。
func IsContinuation(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

// Unfold 把续行并入上一行，前导空白压成一个空格。
func Unfold(lines []string) ([]string, error) {
	var out []string
	for _, line := range lines {
		if IsContinuation(line) {
			if len(out) == 0 {
				return nil, ErrLeadingContinuation
			}
			out[len(out)-1] += " " + strings.TrimLeft(line, " \t")
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// Fold 把一行完整头部（"Name: value"）按宽度重折。
// 在值中的空格处断开，续行以单个空格开头；找不到空格的长行
// 原样输出（宽度是尽力而为，不破坏往返无损）。
func Fold(line string, width int) []string {
	if width <= 0 || len(line) <= width {
		return []string{line}
	}
	var out []string
	rest := line
	for len(rest) > width {
		cut := -1
		for i := min(len(rest)-2, width); i > 0; i-- {
			if rest[i] == ' ' && rest[i+1] != ' ' {
				cut = i
				break
			}
		}
		if cut <= 0 {
			// 第一行不允许在名字里断；找不到空格就放弃本行重折。
			if len(out) == 0 {
				return []string{line}
			}
			break
		}
		out = append(out, rest[:cut])
		rest = " " + rest[cut+1:]
	}
	out = append(out, rest)
	return out
}
