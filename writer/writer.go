// Package writer 做最小引号回写：只在必要时加引号并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断字段是否必须加引号：
// 含逗号 / 引号 / CR / LF，或值为空且原文加了引号（单列空值与空行消歧）。
func needsQuote(c cell.Cell) bool {
	if c.Value == "" {
		return c.Quoted
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// Field 回写单个字段。
func Field(c cell.Cell) string {
	if !needsQuote(c) {
		return c.Value
	}
	var b strings.Builder
	b.Grow(len(c.Value) + 2)
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

// Write 回写整表，统一 \r\n 不做转换（引号内原样，普通字段不含 CR）。
// 每条记录以 \n 结束；规范输入因此总以换行结尾，末尾换行不产生空记录。
func Write(t *table.Table) string {
	var b strings.Builder
	for _, rec := range t.Records {
		for i, c := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(Field(c))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
