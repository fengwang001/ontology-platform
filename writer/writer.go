// Package writer 做最小引号回写：只在必要时加引号并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
)

func needsQuote(v string, singleColumn bool) bool {
	if v == "" && singleColumn {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

// WriteField 回写单个字段：含逗号/引号/CR/LF、或单列空字段时加引号，
// 引号转义为 ""。原本加引号但无必要的多余引号会被去掉（最小引号）。
func WriteField(c cell.Cell, singleColumn bool) string {
	if !needsQuote(c.Value, singleColumn) {
		return c.Value
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
	return b.String()
}

// Write 回写整张表。每条记录以 \n 结束（含最后一条）；空表为空串，
// 因此末尾换行不产生额外记录。
func Write(rows []cell.Record) []byte {
	var b strings.Builder
	single := true
	for _, r := range rows {
		if len(r) != 1 {
			single = false
		}
	}
	for _, r := range rows {
		for i, c := range r {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(WriteField(c, single))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
