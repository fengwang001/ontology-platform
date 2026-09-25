// Package writer 做最小引号回写，保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断未加引号字段何时必须加引号：
// 含逗号、引号、CR、LF，或单列表中的空字段（否则与空行不可区分）。
func needsQuote(v string, singleCol bool) bool {
	if v == "" {
		return singleCol
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

func writeField(b *strings.Builder, c cell.Cell, singleCol bool) {
	q := c.Quoted || needsQuote(c.Value, singleCol)
	if !q {
		b.WriteString(c.Value)
		return
	}
	b.WriteByte('"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			b.WriteByte('"')
		}
		b.WriteByte(c.Value[i])
	}
	b.WriteByte('"')
}

func writeRecord(b *strings.Builder, rec []cell.Cell) {
	single := len(rec) == 1
	for i := range rec {
		if i > 0 {
			b.WriteByte(',')
		}
		writeField(b, rec[i], single)
	}
	b.WriteByte('\n')
}

// Write 把表回写为规范字节序列。
func Write(t table.Table) []byte {
	var b strings.Builder
	if len(t.Header) > 0 {
		writeRecord(&b, t.Header)
	}
	for _, r := range t.Rows {
		writeRecord(&b, r)
	}
	return []byte(b.String())
}
