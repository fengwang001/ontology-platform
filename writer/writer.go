package writer

import (
	"bytes"

	"ontology/cell"
	"ontology/table"
)

func needsQuote(v []byte) bool {
	return bytes.ContainsAny(v, ",\"\r\n")
}

func writeField(buf *bytes.Buffer, c cell.Cell, singleEmpty bool) {
	quote := c.Quoted || needsQuote(c.Value) || singleEmpty
	if !quote {
		buf.Write(c.Value)
		return
	}
	buf.WriteByte('"')
	for _, b := range c.Value {
		if b == '"' {
			buf.WriteByte('"')
		}
		buf.WriteByte(b)
	}
	buf.WriteByte('"')
}

// Write 以最小引号回写一张表。记录均以 \n 结束；
// 单列表中的空字段写为 ""（见 DESIGN.md 推导一）。
func Write(t *table.Table) []byte {
	var buf bytes.Buffer
	for _, r := range t.Rows {
		single := len(r.Cells) == 1 && len(r.Cells[0].Value) == 0
		for i := range r.Cells {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeField(&buf, r.Cells[i], single)
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
