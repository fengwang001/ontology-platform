// Package cell 表示 CSV 字段值及其在原文中的位置。
package cell

import (
	"errors"
	"fmt"
)

// 四类语法错误与孤立 CR：彼此可用 errors.Is 区分。
var (
	ErrBareQuote      = errors.New("cell: bare '\"' in unquoted field")
	ErrQuoteClose     = errors.New("cell: unexpected char after closing quote")
	ErrUnclosedQuote  = errors.New("cell: unclosed quoted field at EOF")
	ErrLoneCR         = errors.New("cell: bare CR not followed by LF")
	ErrFieldTooLong   = errors.New("cell: field exceeds max bytes")
	ErrTooManyFields  = errors.New("cell: record exceeds max fields")
	ErrTooManyRecords = errors.New("cell: table exceeds max records")
)

// Cell 是字段的原始描述。Quoted 区分 "" 与未引号空字段。
type Cell struct {
	Quoted bool // 原文是否被引号包裹
	Start  int  // 首字节偏移（quoted 时为开引号）
	End    int  // 尾字节后偏移（quoted 时为闭引号后）
}

// Raw 返回字段在 buf 中覆盖的原始字节切片。
func (c Cell) Raw(buf []byte) []byte { return buf[c.Start:c.End] }

// Decode 把原始切片解码为字段值；quoted 时去引号并把 "" 折叠为 "。
// 调用方需保证 span 来自合法 CSV；非法转义原样保留。
func Decode(buf []byte, quoted bool) string {
	if !quoted {
		return string(buf)
	}
	s := buf[1 : len(buf)-1]
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '"' && i+1 < len(s) && s[i+1] == '"' {
			i++
		}
		out = append(out, s[i])
	}
	return string(out)
}

// PosError 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at offset %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// At 用位置包装一个哨兵错误。
func At(err error, off, rec, fld int) *PosError {
	return &PosError{Err: err, Offset: off, Record: rec, Field: fld}
}
