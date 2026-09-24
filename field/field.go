// Package field 把单行解析为「字段名: 值」。
package field

import "strings"

// Field 是一个解析出的字段。Name 为空表示注释行（以 ':' 开头）。
type Field struct {
	Name  string
	Value string
}

// Parse 解析一行：无冒号时整行是字段名、值为空串；
// 有冒号时冒号后恰好一个空格被吃掉。
func Parse(line string) Field {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return Field{Name: line}
	}
	v := line[i+1:]
	if len(v) > 0 && v[0] == ' ' {
		v = v[1:]
	}
	return Field{Name: line[:i], Value: v}
}
