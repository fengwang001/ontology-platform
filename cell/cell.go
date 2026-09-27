// Package cell 表示 CSV 的一个字段值及其原文定位信息。
package cell

import "errors"

// Cell 是一条记录中的一个字段。
// Quoted 区分未加引号的空字段与加了引号的空字段 ""。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)，
// 引号字段包含首尾引号，偏移为输入起始 0。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// 四类可彼此区分的语法错误。
var (
	ErrBareQuote       = errors.New("bare double quote in unquoted field")
	ErrQuoteAfterClose = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote   = errors.New("unterminated quoted field")
	ErrLoneCR          = errors.New("bare carriage return not followed by newline")
)

// 三类可彼此区分的上限错误。
var (
	ErrFieldTooLarge = errors.New("field exceeds maximum byte size")
	ErrTooManyFields = errors.New("record exceeds maximum field count")
	ErrTooManyRows   = errors.New("input exceeds maximum record count")
)

// 记录列数与首条记录不一致。
var ErrColumnCount = errors.New("record has wrong number of fields")

// Error 携带出错位置：字节偏移(0 起)、记录号、字段号(均 1 起)。
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

// Is 支持 errors.Is 按哨兵错误判定。
func (e *Error) Is(target error) bool { return e.Err == target }

// Pos 返回字节偏移、记录号、字段号。
func (e *Error) Pos() (offset, record, field int) { return e.Offset, e.Record, e.Field }
