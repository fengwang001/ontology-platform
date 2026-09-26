// Package writer 把表以最小引号回写，保证往返。
package writer

import (
	"strings"

	"ontology/cell"
)

// Records 是行的序列，每行若干字段。
type Records = [][]cell.Cell

// Field 回写单字段。forceEmptyQuote 用于单列表的唯一空字段（见 DESIGN §2）。
func Field(c cell.Cell, forceEmptyQuote bool) string {
	v := c.Value
	if !c.Quoted && !forceEmptyQuote {
		return v
	}
	if c.Quoted || forceEmptyQuote ||
		strings.ContainsAny(v, ",\"\r\n") {
		return "\"" + strings.ReplaceAll(v, "\"", "\"\"") + "\""
	}
	return v
}

// Write 回写整张表，LF 行尾，末记录后恰有一个换行。
// 空表写出空串。
func Write(t Records) []byte {
	var b strings.Builder
	single := false
	if len(t) > 0 {
		single = len(t[0]) == 1
	}
	for _, rec := range t {
		for i, c := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(Field(c, single && len(rec) == 1))
		}
		b.WriteByte('\n')
}
	return []byte(b.String())
}
