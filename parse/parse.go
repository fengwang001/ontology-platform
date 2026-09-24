// Package parse 把 source 的原始行解析成记录；坏记录由调用方计数，不中断管线。
package parse

import (
	"errors"

	"ontology/source"
)

// Record 是一条解析成功的记录。空键合法（":5"）。
type Record struct {
	Key string
	Val int64
	Off int64
}

// ErrBad 表示坏记录（缺键分隔符或缺数值等）。
var ErrBad = errors.New("parse: bad record")

// Parse 解析一行。无冒号（缺失键位）、空值、非数字值均判坏。
// 形如 ":5" 的行是空键 + 数值 5，合法。
func Parse(line string, off int64) (Record, error) {
	idx := -1
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(line)-1 {
		return Record{}, ErrBad
	}
	val, err := atoi(line[idx+1:])
	if err != nil {
		return Record{}, ErrBad
	}
	return Record{Key: line[:idx], Val: val, Off: off}, nil
}

// ParseItem 适配 source.Item。
func ParseItem(it source.Item) (Record, error) {
	return Parse(it.Line, it.Off)
}

func atoi(s string) (int64, error) {
	if s == "" {
		return 0, ErrBad
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, ErrBad
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrBad
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
