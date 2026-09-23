// Package writer 把表以最小引号方式回写为 RFC4180 方言 CSV。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断字段是否「必须」加引号。
// 必要情形：含逗号、引号、CR、LF；以及单列表中的空字段（否则会被当作
// 空行跳过，见 DESIGN.md 第 1 节）。
func needsQuote(v string, ncols int) bool {
	if v == "" && ncols == 1 {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

func writeField(c cell.Cell, ncols int) string {
	if !c.Quoted && !needsQuote(c.Value, ncols) {
		return c.Value
	}
	var b strings.Builder
	b.Grow(len(c.Value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			b.WriteByte('"')
		}
		b.WriteByte(c.Value[i])
	}
	b.WriteByte('"')
	return b.String()
}

// Write 以规范形式输出表：记录间一个 \n，末尾无换行，字段内 \r\n 原样保留。
func Write(t *table.Table) []byte {
	ncols := len(t.Header)
	var b strings.Builder
	writeRec := func(rec []cell.Cell) {
		for i, c := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(writeField(c, ncols))
		}
	}
	first := true
	emit := func(rec []cell.Cell) {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		writeRec(rec)
	}
	emit(t.Header)
	for _, r := range t.Rows {
		emit(r)
	}
	return []byte(b.String())
}
