// Package writer 做最小引号 CSV 回写。
package writer

import (
	"strings"

	"ontology/cell"
)

// needsQuote 推导"必要引号"：空串，或含 , " \r \n。
func needsQuote(s string) bool {
	return s == "" || strings.ContainsAny(s, ",\"\r\n")
}

// Cell 回写单个字段。
func Cell(c cell.Cell) string { return "" }

// Record 回写一条记录（逗号连接，CRLF 结尾）。
func Record(fields []cell.Cell) string { return "" }

// Write 回写整表。
func Write(records [][]cell.Cell) []byte { return nil }
