// Package writer 做最小引号回写并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needQuote 判定一个字段在 ncol 列表里是否必须加引号：
// 值含逗号/引号/CR/LF 一律加；单列空字段（值为空）必须加引号，
// 否则该记录与空行不可区分，往返丢行。
func needQuote(c cell.Cell, ncol int) bool {
	if c.Quoted && c.Value == "" && ncol == 1 {
		return true
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// writeField 回写单字段：只在必要时加引号，引号转义为 ""。
func writeField(b *strings.Builder, c cell.Cell, ncol int) {
	if !needQuote(c, ncol) {
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

// writeRow 回写一行（不含行尾）。
func writeRow(b *strings.Builder, cells []cell.Cell) {
	for i := range cells {
		if i > 0 {
			b.WriteByte(',')
		}
		writeField(b, cells[i], len(cells))
	}
}

// Write 把表序列化为 CSV；header 非空时作为首行。
// 空表写为空字符串；记录间一个 \n，无尾随换行。
func Write(t *table.Table) string {
	var b strings.Builder
	nrow := len(t.Rows)
	if len(t.Header) > 0 {
		nrow++
	}
	for i := 0; i < nrow; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		var cells []cell.Cell
		if len(t.Header) > 0 && i == 0 {
			cells = make([]cell.Cell, len(t.Header))
			for j, h := range t.Header {
				cells[j] = cell.Cell{Value: h}
			}
		} else {
			r := t.Rows[i]
			if len(t.Header) > 0 {
				r = t.Rows[i-1]
			}
			cells = r.Cells
		}
		writeRow(&b, cells)
	}
	return b.String()
}
