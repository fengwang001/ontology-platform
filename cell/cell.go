// Package cell 表示一个 CSV 字段值：内容、是否带引号、原文字节区间 [Start,End)。
package cell

import "errors"

// Cell 是一条记录中的一个字段。Quoted 区分未引号空字段与 ""。
type Cell struct {
	Value  string
	Quoted bool
	Start  int // 原文起始字节偏移（含开引号）
	End    int // 原文结束字节偏移（不含闭引号后的分隔符）
}

// 哨兵错误：四类语法错误彼此可区分，孤立 \r 单独一类。
var (
	ErrBareQuote       = errors.New("bare field contains a quote")
	ErrCharsAfterQuote = errors.New("unexpected chars after closing quote")
	ErrUnclosedQuote   = errors.New("unclosed quoted field at end of input")
	ErrLoneCR          = errors.New("lone carriage return not followed by newline")
	ErrColumnCount     = errors.New("record field count differs from header")
	ErrFieldTooLarge   = errors.New("field exceeds max bytes")
	ErrTooManyFields   = errors.New("record exceeds max field count")
	ErrTooManyRecords  = errors.New("table exceeds max record count")
)

// Error 携带出错位置：字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// NewError 包装一个哨兵错误并附带位置。
func NewError(err error, offset, record, field int) *Error {
	return &Error{Err: err, Offset: offset, Record: record, Field: field}
}
