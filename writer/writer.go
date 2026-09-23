// Package writer 做最小引号回写，并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 推导「必须加引号」的情形：
// 字段含逗号、引号、\r、\n 时不加引号会破坏分隔/转义；
// 单列（n==1）且值为空时，裸写为空字节会被读端当作空行跳过，整条记录丢失。
func needsQuote(c cell.Cell, n int) bool {
	if c.Quoted {
		return true // 保留原文引号标记
	}
	if n == 1 && len(c.Value) == 0 {
		return true
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// Cell 回写单个字段。
func Cell(c cell.Cell, n int) string {
	if !needsQuote(c, n) {
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

// Record 回写一行（不含行尾）。
func Record(r table.Record) string {
	parts := make([]string, len(r))
	for i, c := range r {
		parts[i] = Cell(c, len(r))
	}
	return strings.Join(parts, ",")
}

// Write 回写整张表：每条记录以 \n 结束（规范形式，含最后一条）。
func Write(t *table.Table) []byte {
	var b strings.Builder
	for _, r := range t.Records {
		b.WriteString(Record(r))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
