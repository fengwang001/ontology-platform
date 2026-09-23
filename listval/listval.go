// Package listval 处理逗号分隔的列表型头部值。
//
// 切分规则：逗号只在引号字符串外作为分隔符；引号内反斜杠转义下一字符
// （因此 \" 不闭合字符串）。每项首尾 OWS 去除。空项（如 "a,,b" 中间、
// 或首尾逗号产生的空元素）判定为语法错误——选择拒绝而非静默丢弃，
// 与全包的"不静默改数据"原则一致。合并用 ", " 连接，引号原样保留，
// 因此 切分→合并→再切分 得到同一元素序列（语义等价）。
package listval

import (
	"errors"
	"strings"
)

// ErrEmptyItem 表示列表中出现空元素（如 "a,,b"、",a"、"a,"）。
var ErrEmptyItem = errors.New("listval: empty list item")

// ErrUnclosedQuote 表示引号字符串未闭合。
var ErrUnclosedQuote = errors.New("listval: unclosed quoted-string")

// Split 把列表值切成元素。引号（含转义）原样保留在元素内。
func Split(v string) ([]string, error) {
	var items []string
	var b strings.Builder
	inQuote := false
	escaped := false
	flush := func() error {
		item := strings.Trim(b.String(), " \t")
		b.Reset()
		if item == "" {
			return ErrEmptyItem
		}
		items = append(items, item)
		return nil
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case inQuote && c == '\\':
			b.WriteByte(c)
			escaped = true
		case c == '"':
			b.WriteByte(c)
			inQuote = !inQuote
		case c == ',' && !inQuote:
			if err := flush(); err != nil {
				return nil, err
			}
		default:
			b.WriteByte(c)
		}
	}
	if inQuote || escaped {
		return nil, ErrUnclosedQuote
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return items, nil
}

// Join 把元素合并回列表值。元素先 trim；空元素同样拒绝。
func Join(items []string) (string, error) {
	trimmed := make([]string, len(items))
	for i, it := range items {
		trimmed[i] = strings.Trim(it, " \t")
		if trimmed[i] == "" {
			return "", ErrEmptyItem
		}
	}
	return strings.Join(trimmed, ", "), nil
}
