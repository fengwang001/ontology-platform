// Package writer 做最小引号回写：只在必要时加引号并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判定字段是否必须加引号：
// 含逗号/引号/CR/LF，或单列表中的空字段（否则会被当成空行跳过）。
func needsQuote(c cell.Cell, width int) bool {
	if c.Quoted {
		return true // 保留原文引号标记，保证引号标记往返一致
	}
	if width == 1 && c.Value == "" {
		return true
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// writeField 回写单字段。
func writeField(b *strings.Builder, c cell.Cell, width int) {
	if !needsQuote(c, width) {
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

// Write 把表回写为 CSV；每条记录以 LF 结尾（规范形式）。
func Write(t *table.Table) []byte {
	rows := append([][]cell.Cell{t.Header}, t.Records...)
	var b strings.Builder
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		for i, c := range row {
			if i > 0 {
				b.WriteByte(',')
			}
			writeField(&b, c, len(row))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
