// Package writer 用最小引号把表回写为 CSV，并保证往返。
package writer

import (
	"bytes"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判定最小引号规则。
// 必要情形：内容含 , " \r \n；单列记录唯一字段为空（否则与空行/无字段流尾
// 不可区分，会丢记录）；该字段原文就带引号（为了引号标记往返保真）。
func needsQuote(v []byte, quoted bool, cols int) bool {
	if bytes.IndexAny(v, ",\"\r\n") >= 0 {
		return true
	}
	if cols == 1 && len(v) == 0 {
		return true
	}
	return quoted
}

func writeField(buf *bytes.Buffer, c cell.Cell, cols int) {
	if needsQuote(c.Value, c.Quoted, cols) {
		buf.WriteByte('"')
		for _, b := range c.Value {
			if b == '"' {
				buf.WriteByte('"')
			}
			buf.WriteByte(b)
		}
		buf.WriteByte('"')
		return
	}
	buf.Write(c.Value)
}

// Write 把表序列化为规范 CSV：记录间用 \n，末尾无换行。
// 字段原本带引号但内容不需要引号时，按最小引号规则去掉引号
// （Parse 后值与引号标记在该情形下仍一致：解析器不会对其重建引号）。
func Write(t *table.Table) []byte {
	var buf bytes.Buffer
	writeRec := func(rec []cell.Cell, first *bool) {
		if !*first {
			buf.WriteByte('\n')
		}
		*first = false
		for i := range rec {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeField(&buf, rec[i], len(rec))
		}
	}
	first := true
	if t.Header != nil {
		writeRec(t.Header, &first)
	}
	for i := range t.Rows {
		writeRec(t.Rows[i], &first)
	}
	return buf.Bytes()
}
