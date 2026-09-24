package cell

import (
	"errors"
	"fmt"
)

// Cell 是一个 CSV 字段。Quoted 区分裸空字段与 ""，Start/End 是原文字节偏移 [Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Limits 是可配置的解析上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int64
	MaxFields     int
	MaxRecords    int
}

var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrAfterQuote    = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrLoneCR        = errors.New("bare carriage return not followed by newline")
	ErrFieldLimit    = errors.New("field byte limit exceeded")
	ErrFieldsLimit   = errors.New("field count per record exceeded")
	ErrRecordsLimit  = errors.New("record count exceeded")
	ErrColumnCount   = errors.New("record field count differs from header")
	ErrTerminal      = errors.New("parser already in terminal error state")
)

// Error 携带出错位置：字节偏移（从 0 起）、记录号与字段号（从 1 起，0 表示未知）。
type Error struct {
	Err    error
	Offset int64
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}

func (e *Error) Unwrap() error { return e.Err }

// At 用记录号/字段号补全词法错误的位置信息。
func (e *Error) At(record, field int) *Error {
	cp := *e
	cp.Record, cp.Field = record, field
	return &cp
}

// NewError 构造一个带字节偏移的错误。
func NewError(err error, offset int64) *Error {
	return &Error{Err: err, Offset: offset}
}
