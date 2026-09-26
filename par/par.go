// Package par 按任意字节偏移切分缓冲区，并行解析后拼接。
package par

import "ontology/table"

// Stats 报告并行解析统计。
type Stats struct{ BytesProcessed int }

// Parse 把 buf 尽量均等地切成 K 段并行解析。
func Parse(buf []byte, k int, opts table.Options) (*table.Table, Stats, error) {
	return &table.Table{}, Stats{}, nil
}

// ParseCuts 使用显式切点（切点为各段起始偏移，含 0）。
func ParseCuts(buf []byte, cuts []int, opts table.Options) (*table.Table, Stats, error) {
	return &table.Table{}, Stats{}, nil
}
