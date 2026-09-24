// Package table 把 lexer 事件组装成记录与表头。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 为解析结果。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Reader 是流式组装器（占位）。
type Reader struct{}

func NewReader(_ lexer.Limits) *Reader { return &Reader{} }
