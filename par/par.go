// Package par 按任意字节偏移切分缓冲区并行解析后拼接。
package par

import "ontology/table"

// Result 是并行解析结果。
type Result struct {
	Table          *table.Table
	Err            error
	BytesProcessed int
}

// Parse 把 buf 切成 K 段并行解析，结果与单线程流式解析一致。
func Parse(buf []byte, k int) *Result { return &Result{} }
