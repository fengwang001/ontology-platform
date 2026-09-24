// Package parse 把原始字节帧解析成聚合记录，坏帧只计数不中断管线。
package parse

import (
	"bytes"
	"strconv"

	"ontology/source"
)

// Record 是一条解析成功的记录。Key 允许为空串。
type Record struct {
	Pos int64
	Key string
	Val int64
}

// Parser 解析帧。坏帧计入 Bad，不返回错误。
type Parser struct {
	Bad int64
}

// Parse 解析一帧。第二个返回值表示是否成功。
func (p *Parser) Parse(f source.Frame) (Record, bool) {
	data := bytes.TrimSpace(f.Data)
	idx := bytes.IndexByte(data, '=')
	if idx < 0 {
		p.Bad++
		return Record{}, false
	}
	val, err := strconv.ParseInt(string(data[idx+1:]), 10, 64)
	if err != nil {
		p.Bad++
		return Record{}, false
	}
	return Record{Pos: f.Pos, Key: string(data[:idx]), Val: val}, true
}
