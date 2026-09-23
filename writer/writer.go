// Package writer 做最小引号回写，保证 Parse(Write(T)) 与 T 相等。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

func field(b *strings.Builder, c cell.Cell) {
	if !c.Quoted {
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

// Write 以 \n 为记录分隔符序列化表，每条记录（含末条）后都有换行。
// 仅当字段的 Quoted 标记为真才加引号，即「最小引号」。
func Write(t *table.Table) string {
	var b strings.Builder
	for _, row := range t.Rows {
		for i := range row {
			if i > 0 {
				b.WriteByte(',')
			}
			field(&b, row[i])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Cell 生成单字段的规范字面（供需要逐字段回写的调用方使用）。
func Cell(c cell.Cell) string {
	var b strings.Builder
	field(&b, c)
	return b.String()
}
