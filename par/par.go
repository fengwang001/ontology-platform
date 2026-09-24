// Package par 把缓冲区任意切 K 段并行解析后拼接。
package par

import (
	"ontology/lexer"
	"ontology/table"
)

// Parse 并行解析（占位）。
func Parse(buf []byte, k int, lim lexer.Limits) (*table.Table, error) { return nil, nil }
