// Package parse 把原始字节解析成键值记录。
package parse

import (
	"bytes"
	"strconv"
)

// Record 是一条解析后的记录。
type Record struct {
	Key string
	Val int64
}

// ErrBad 表示坏记录（调用方按 error 判断，不作为流水线致命错误）。
var ErrBad = badRecordErr{}

type badRecordErr struct{}

func (badRecordErr) Error() string { return "bad record" }

// Parse 解析一行原始字节，格式为 "key:number"。
// 缺失冒号（无键）、空键之外的缺键、数字非法都算坏记录；空键合法。
func Parse(raw []byte) (Record, error) {
	line := string(bytes.TrimRight(raw, "\n"))
	i := bytes.IndexByte(raw, ':')
	if i <= 0 {
		return Record{}, ErrBad
	}
	key := line[:i]
	val, err := strconv.ParseInt(line[i+1:], 10, 64)
	if err != nil {
		return Record{}, ErrBad
	}
	return Record{Key: key, Val: val}, nil
}
