// Package parse 把原始字节解析成记录。坏记录单独计数，不中断管线。
// 记录格式： "<offset>|<key>|<value>"，三段以 '|' 分隔。
// key 允许空串；字段缺失或数值非法即坏记录。
package parse

import (
	"errors"
	"strconv"
	"strings"
)

// ErrBad 表示一条坏记录（由调用方计数，不作为管线错误）。
var ErrBad = errors.New("parse: bad record")

// Record 是一条解析成功的记录。
type Record struct {
	Off   int
	Key   string
	Value int64
}

// Parse 解析一条原始记录。ok=false 表示坏记录。
func Parse(line []byte) (Record, bool) {
	s := string(line)
	parts := strings.SplitN(s, "|", 3)
	if len(parts) != 3 {
		return Record{}, false
	}
	off, err := strconv.Atoi(parts[0])
	if err != nil {
		return Record{}, false
	}
	key := parts[1] // 空串合法；缺段（少于3段）已在上面判坏
	val, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Record{}, false
	}
	return Record{Off: off, Key: key, Value: val}, true
}
