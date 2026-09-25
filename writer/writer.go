// Package writer 以最小引号回写表，保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 推导"必要引号"：含 ,、"、\r、\n；以及单列表空值。
// 单列表空值不加引号写出就是空行，再解析会被空行规则跳过 → 丢记录，
// 故单列表中值为空的字段（无论原引号与否）必须写成 ""。
func needsQuote(c cell.Cell, singleColumn bool) bool {
	v := c.Value
	if v == "" {
		return singleColumn
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

// WriteCell 回写单字段：仅在必要时加引号；引号字段内的 " 翻倍。
func WriteCell(c cell.Cell, singleColumn bool) []byte {
	if !needsQuote(c, singleColumn) {
		return []byte(c.Value)
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
	return []byte(b.String())
}

// Write 回写为规范 CSV：\n 分隔记录、无尾换行。
func Write(t *table.Table) []byte {
	var b strings.Builder
	single := len(t.Records) > 0 && len(t.Records[0]) == 1
	for r, rec := range t.Records {
		if r > 0 {
			b.WriteByte('\n')
		}
		for i := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(WriteCell(rec[i], single))
		}
	}
	return []byte(b.String())
}
