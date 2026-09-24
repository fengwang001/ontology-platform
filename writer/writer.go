// Package writer 用最小引号把表回写为 CSV（canonical 形式，见 DESIGN.md §5）。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断字段值是否必须加引号：含 , " \r \n；
// 单列记录的唯一空字段也必须写 ""，否则空行会被解析抑制而丢记录。
func needsQuote(v string, singleEmpty bool) bool {
	if singleEmpty && v == "" {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

func writeField(b *strings.Builder, c cell.Cell, forceQuotedEmpty bool) {
	q := c.Quoted
	if needsQuote(c.Value, forceQuotedEmpty) {
		q = true
	}
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

// Write 将表序列化为 canonical CSV：记录间一个 \n，结尾无换行。
func Write(t *table.Table) []byte {
	recs := t.Records()
	var b strings.Builder
	for ri, rec := range recs {
		single := len(rec) == 1
		for ci := range rec {
			if ci > 0 {
				b.WriteByte(',')
			}
			writeField(&b, rec[ci], single)
		}
		if ri+1 < len(recs) {
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}
