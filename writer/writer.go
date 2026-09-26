// Package writer 做最小引号回写。
package writer

import (
	"strings"

	"ontology/cell"
)

// Write 把表回写为规范 CSV 字节。
func Write(header []string, rows [][]cell.Cell) string {
	var b strings.Builder
	return b.String()
}
