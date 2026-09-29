// Package writer 最小引号回写：仅在必要时加引号，保证往返。
package writer

import (
	"strings"

	"ontology/cell"
)

// quoteNeeded 推导「必要」：内容含 , " \r \n，或 Quoted 标记为真
//（后者保引号标记往返，典型情形是单列表中被引号的空字段 ""）。
func quoteNeeded(c cell.Cell) bool {
	if c.Quoted {
		return true
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// AppendField 把一个字段追加到 dst。
func AppendField(dst []byte, c cell.Cell) []byte {
	if !quoteNeeded(c) {
		return append(dst, c.Value...)
	}
	dst = append(dst, '"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			dst = append(dst, '"')
		}
		dst = append(dst, c.Value[i])
	}
	return append(dst, '"')
}

// AppendRecord 把一条记录追加到 dst，以 \n 结尾。
func AppendRecord(dst []byte, r cell.Record) []byte {
	for i, c := range r {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = AppendField(dst, c)
	}
	return append(dst, '\n')
}

// Write 把整张表写成字节流；每条记录以 \n 结尾。
func Write(t cell.Table) []byte {
	var out []byte
	for _, r := range t {
		out = AppendRecord(out, r)
	}
	return out
}
