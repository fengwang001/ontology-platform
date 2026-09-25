// Package writer 做最小引号回写。
package writer

import (
	"strings"

	"ontology/cell"
)

// Write 把表回写为 CSV，保证往返。
func Write(records [][]cell.Cell) []byte {
	var b strings.Builder
	for _, rec := range records {
		for i, c := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(render(c, len(rec) == 1))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func render(c cell.Cell, single bool) string {
	v := c.Value
	need := strings.ContainsAny(v, ",\"\r\n") || (single && v == "" && !c.Quoted)
	if !need {
		return v
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		if v[i] == '"' {
			b.WriteByte('"')
		}
		b.WriteByte(v[i])
	}
	b.WriteByte('"')
	return b.String()
}
