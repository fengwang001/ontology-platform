// Package writer 把表回写为 CSV：只在必要时加引号，保证往返。
package writer

import (
	"strings"

	"ontology/cell"
)

// needsQuote 判断字段是否必须加引号：
// 含逗号、引号、CR、LF；单列空字段必须写为 ""（见 DESIGN.md 第 1 节）。
// 用户显式标记引号（c.Quoted）时同样保留引号以维持引号标记往返。
func needsQuote(v string, width int) bool {
	if v == "" && width == 1 {
		return true
	}
	return strings.ContainsAny(v, ",\"\r\n")
}

// writeCell 回写单个字段。
func writeCell(c cell.Cell, width int) string {
	if !c.Quoted && !needsQuote(c.Value, width) {
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

// Write 把记录表回写为 CSV 字节。
// 记录间以 \n 分隔，最后一条记录后也以 \n 结束（非空表时）。
func Write(records [][]cell.Cell) []byte {
	if len(records) == 0 {
		return nil
	}
	var b strings.Builder
	width := len(records[0])
	for r, rec := range records {
		if r > 0 {
			b.WriteByte('\n')
		}
		for i, c := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(writeCell(c, width))
		}
	}
	b.WriteByte('\n')
	return []byte(b.String())
}
