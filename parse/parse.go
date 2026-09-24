package parse

import (
	"errors"
	"strconv"
	"strings"
)

// Record 是一条解析成功的记录。
type Record struct {
	Offset int64
	Key    string
	Value  int64
}

// ErrBad 表示坏记录：缺少分隔符（键缺失）或值不是整数。
var ErrBad = errors.New("parse: bad record")

// Parse 解析形如 "key=value" 的字节串。
// 没有 '=' 视为键缺失 → ErrBad；键为空串（"=3"）合法；
// 值必须是十进制整数，否则 ErrBad。
func Parse(offset int64, data []byte) (Record, error) {
	line := string(data)
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return Record{}, ErrBad
	}
	key := line[:eq]
	value, err := strconv.ParseInt(line[eq+1:], 10, 64)
	if err != nil {
		return Record{}, ErrBad
	}
	return Record{Offset: offset, Key: key, Value: value}, nil
}

// Counter 统计坏记录数，可并发读出。
type Counter struct{ bad int64 }

// Add 累加一条坏记录。
func (c *Counter) Add() { c.bad++ }

// Bad 返回累计坏记录数。
func (c *Counter) Bad() int64 { return c.bad }
