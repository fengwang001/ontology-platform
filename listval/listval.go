// Package listval 切分与合并列表型头部值（逗号分隔，支持引号字符串）。
// 依赖 token 包。
package listval

import (
	"errors"
	"strings"
)

var (
	// ErrEmptyItem 表示出现空项：a,,b、以逗号开头或以逗号结尾。
	// 本包选定的语义是"空项判错"：标准列表头部中空元素无合法语义，
	// 容忍它会让"缺值"与"空值"无法区分。
	ErrEmptyItem = errors.New("listval: empty list item")
	// ErrUnterminatedQuote 表示引号字符串未闭合。
	ErrUnterminatedQuote = errors.New("listval: unterminated quoted string")
)

// Split 按顶层逗号切分。引号内逗号不切分；引号由前导奇数个反斜杠判定
// 是否转义（\" 不切换引号状态）。每项首尾 OWS 去除，项体（含引号）原样
// 保留，参数（;...）随其所属项保留。空项返回 ErrEmptyItem。
func Split(v string) ([]string, error) {
	var items []string
	depth := 0
	start := 0
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '"':
			if !escaped(v, i) {
				depth ^= 1
			}
		case ',':
			if depth == 0 {
				item := strings.TrimFunc(v[start:i], isOWS)
				if item == "" {
					return nil, ErrEmptyItem
				}
				items = append(items, item)
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, ErrUnterminatedQuote
	}
	item := strings.TrimFunc(v[start:], isOWS)
	if item == "" {
		return nil, ErrEmptyItem
	}
	return append(items, item), nil
}

// Join 用 ", " 合并元素。元素原样连接（不在此处做转义），因此
// Split(Join(Split(v))) 得到完全相同的元素序列（语义等价往返）。
func Join(items []string) string {
	return strings.Join(items, ", ")
}

// escaped 报告位置 i 的引号/反斜杠是否被前导连续反斜杠转义。
func escaped(v string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && v[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func isOWS(r rune) bool { return r == ' ' || r == '\t' }
