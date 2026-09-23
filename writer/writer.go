// Package writer 做最小引号 CSV 回写，保证与解析器往返一致。仅依赖 cell。
package writer

import (
	"strings"

	"ontology/cell"
)

// needsQuote 判定一个字段在单列表(sole)中是否必须加引号。
// 必要情形：含逗号/引号/CR/LF，或单列唯一字段为空（否则会被当空行跳过）。
func needsQuote(v string, sole bool) bool {
	if sole && v == "" {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

// Cell 回写单个字段：保留原引号标记，或在必要时加引号。
func Cell(c cell.Cell, sole bool) string {
	if !c.Quoted && !needsQuote(c.Value, sole) {
		return c.Value
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range c.Value {
		if r == '"' {
			b.WriteByte('"')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// Record 回写一条记录（不含行尾）。
func Record(r cell.Record) string {
	sole := len(r) == 1
	parts := make([]string, len(r))
	for i, c := range r {
		parts[i] = Cell(c, sole)
	}
	return strings.Join(parts, ",")
}

// Write 回写整张表：每条记录（含末条）以 \n 结束。
func Write(records []cell.Record) []byte {
	var b strings.Builder
	for _, r := range records {
		b.WriteString(Record(r))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
