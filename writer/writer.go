// Package writer 最小引号回写：只在必要时加引号，并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// Write 把表写回 CSV 字节流；每条记录以 \n 结束。
func Write(t *table.Table) []byte {
	var buf []byte
	writeRec := func(rec []cell.Cell) {
		for i, c := range rec {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCell(buf, c, t.NCol() == 1)
		}
		buf = append(buf, '\n')
	}
	if t.Header != nil {
		writeRec(t.Header)
	}
	for _, r := range t.Rows {
		writeRec(r)
	}
	return buf
}

// appendCell 最小引号规则：Quoted 标记、单列表空值，或值含 , " \r \n 时加引号。
func appendCell(buf []byte, c cell.Cell, solo bool) []byte {
	need := c.Quoted || (solo && c.Value == "") || strings.ContainsAny(c.Value, ",\"\r\n")
	if !need {
		return append(buf, c.Value...)
	}
	buf = append(buf, '"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			buf = append(buf, '"')
		}
		buf = append(buf, c.Value[i])
	}
	return append(buf, '"')
}
