// Package listval 实现列表型头部的切分与合并。
//
// 逗号分隔，但引号字符串内的逗号不切分，\" 转义正确处理；
// 每项去首尾空白；空项（a,,b）在切分时丢弃（与 RFC 9110 对
// 列表空项"必须忽略"的规定一致）。合并用 ", " 连接，保证
// "切分→合并→再切分"得到相同项序列（语义等价往返）。
package listval

import (
	"errors"
	"strings"
)

// ErrUnterminatedQuote 表示引号字符串未闭合。
var ErrUnterminatedQuote = errors.New("listval: unterminated quoted-string")

// Split 按逗号切分列表值，引号内逗号不切，空项丢弃。
func Split(s string) ([]string, error) {
	var items []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	flush := func() {
		item := strings.TrimSpace(cur.String())
		cur.Reset()
		if item != "" {
			items = append(items, item)
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case inQuote && c == '\\':
			cur.WriteByte(c)
			escaped = true
		case c == '"':
			cur.WriteByte(c)
			inQuote = !inQuote
		case c == ',' && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote || escaped {
		return nil, ErrUnterminatedQuote
	}
	flush()
	return items, nil
}

// Join 把列表项合并回单个值，用 ", " 连接。
func Join(items []string) string {
	return strings.Join(items, ", ")
}
