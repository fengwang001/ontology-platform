// Package parse 把原始字节解析为记录；坏记录单独计数，不中断管线。
package parse

import (
	"errors"
	"strconv"
	"strings"
)

// ErrBad 表示一条无法解析的坏记录。
var ErrBad = errors.New("parse: bad record")

// Record 是解析后的记录。空串 Key 合法；无法取得键的记录是坏记录。
type Record struct {
	Key string
	Val float64
}

// Parse 解析形如 "key:1.5" 的单行。
// 没有冒号（键缺失）或值不是数字都判为坏记录；":1.5" 的空串键合法。
func Parse(b []byte) (Record, error) {
	s := string(b)
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return Record{}, ErrBad
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s[i+1:]), 64)
	if err != nil {
		return Record{}, ErrBad
	}
	return Record{Key: s[:i], Val: v}, nil
}

// CountBad 统计一批字节里的坏记录条数，便于无管线场景复用。
func CountBad(rows [][]byte) int {
	bad := 0
	for _, r := range rows {
		if _, err := Parse(r); err != nil {
			bad++
		}
	}
	return bad
}
