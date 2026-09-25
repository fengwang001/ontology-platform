// Package writer 做最小引号回写，并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
)

func needsQuote(c cell.Cell, singleCol bool) bool {
	if c.Quoted {
		return true
	}
	v := c.Value
	if strings.ContainsAny(v, ",\"\r\n") {
		return true
	}
	return singleCol && v == ""
}

func writeCell(b *strings.Builder, c cell.Cell, singleCol bool) {
	if !needsQuote(c, singleCol) {
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
		writeCell(b, rec[i], single)
	}
	b.WriteByte('\n')
}

// Write 把表头与行回写为规范 CSV。
func Write(header, rows [][]cell.Cell) []byte {
	var b strings.Builder
	if len(header) > 0 {
		writeRecord(&b, header)
	}
	for _, r := range rows {
		writeRecord(&b, r)
	}
	return []byte(b.String())
}
