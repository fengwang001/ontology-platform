// Package par 把整段缓冲按任意字节偏移切 K 段并行解析。
package par

import (
	"ontology/cell"
	"ontology/table"
)

// Result 为并行解析结果。
type Result struct {
	Table     *table.Table
	Err       error
	BytesSeen int
}

// Parse 把 buf 切成 K 段并行解析后拼接。
func Parse(buf []byte, k int, opt table.Options) (*Result, error) { return nil, nil }

var _ = cell.Cell{}
