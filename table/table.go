// Package table 把词法事件组装成记录表。
package table

import "ontology/cell"

// Table 是解析结果。Header 为 nil 表示无表头模式。
type Table struct {
	Header []string
	Rows   [][]cell.Cell
}

// Limits 限制资源上限；零值表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Options 控制解析行为。
type Options struct {
	Header bool
	Limits Limits
}

// Parse 一次性解析完整输入。
func Parse(p []byte, opts Options) (*Table, error) { return &Table{}, nil }
