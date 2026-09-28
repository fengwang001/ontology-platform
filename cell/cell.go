// Package cell 表示 CSV 字段值及其在原文中的位置。
package cell

import "errors"

// Cell 是一个字段。Quoted 区分未引号空字段与 ""（加引号空字段）。
// Start/End 为字段在原始字节流中的半开区间偏移 [Start, End)，
// 引号字段含外层引号；EOF 处零宽字段 Start==End。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// 四类彼此可区分的语法/结构错误，外加三类上限错误与终态错误。
var (
	ErrBareQuote      = errors.New("csv: bare '\"' in unquoted field")
	ErrQuoteNotDelim  = errors.New("csv: char after closing quote")
	ErrUnclosedQuote  = errors.New("csv: unclosed quoted field")
	ErrBareCR         = errors.New("csv: bare carriage return")
	ErrColumnCount    = errors.New("csv: wrong field count")
	ErrFieldTooLong   = errors.New("csv: field exceeds byte limit")
	ErrTooManyFields  = errors.New("csv: record exceeds field limit")
	ErrTooManyRecords = errors.New("csv: too many records")
	ErrTerminal       = errors.New("csv: parser in terminal error state")
)

// Error 携带字节偏移（从 0 起）与记录号、字段号（从 1 起）。
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

// At 用当前位置包装哨兵错误。
func At(err error, offset, record, field int) *Error {
	return &Error{Err: err, Offset: offset, Record: record, Field: field}
}
