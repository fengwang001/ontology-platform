// Package writer 把表最小引号回写为 CSV 字节并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

func needsQuote(c cell.Cell, singleCol, lastRecord bool) bool {
	if c.Quoted {
		return true // 保留原文引号标记
	}
	v := c.Value
	if strings.ContainsAny(v, ",\"\r\n") {
		return true
	}
	// 单列表最后一条记录的唯一空字段：不加引号会与尾换行/零记录歧义。
	if v == "" && singleCol && lastRecord {
		return true
	}
	return false
}

func writeCell(b []byte, c cell.Cell, singleCol, last bool) []byte {
	if !needsQuote(c, singleCol, last) {
		return append(b, c.Value...)
	}
	b = append(b, '"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			b = append(b, '"', '"')
		} else {
			b = append(b, c.Value[i])
		}
	}
	return append(b, '"')
}

// Write 回写整表：每条记录以 \n 结束（含最后一条）。
// 引号字段内的 \r\n 原样保留。
func Write(t *table.Table) []byte {
	recs := t.Records
	b := make([]byte, 0, 64)
	for ri, rec := range recs {
		single := len(rec) == 1
		last := ri == len(recs)-1
		for ci, c := range rec {
			if ci > 0 {
				b = append(b, ',')
			}
			b = writeCell(b, c, single, last)
		}
		b = append(b, '\n')
	}
	return b
}

// WriteCanonical 回写规范形态：记录以 \n 分隔、无尾换行、仅必要引号。
func WriteCanonical(t *table.Table) []byte {
	recs := t.Records
	b := make([]byte, 0, 64)
	for ri, rec := range recs {
		if ri > 0 {
			b = append(b, '\n')
		}
		single := len(rec) == 1
		last := ri == len(recs)-1
		for ci, c := range rec {
			if ci > 0 {
				b = append(b, ',')
			}
			b = writeCell(b, c, single, last)
		}
	}
	return b
}
