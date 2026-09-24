// Package writer 是最小引号回写器，保证 Parse(Write(T)) 与 T 一致。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断不加引号是否会破坏往返。
// 必要情形：字段为空（单列空值若不引会被当成空行跳过），或含逗号、引号、\r、\n。
func needsQuote(v string) bool {
	if len(v) == 0 {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

// Field 回写单个字段：只在必要时加引号；若原字段带引号也保留（引号标记往返所需）。
func Field(c cell.Cell) string {
	if !c.Quoted && !needsQuote(c.Value) {
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

// Record 回写一条记录（不含行尾）。
func Record(rec []cell.Cell) string {
	parts := make([]string, len(rec))
	for i, c := range rec {
		parts[i] = Field(c)
	}
	return strings.Join(parts, ",")
}

// Write 回写整张表。规范输出：每条记录以 \n 结束（含最后一条），最小引号。
func Write(t *table.Table) []byte {
	recs := t.Records()
	var b strings.Builder
	for _, rec := range recs {
		b.WriteString(Record(rec))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
