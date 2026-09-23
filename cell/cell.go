// Package cell 表示 CSV 单个字段：解码后的值、是否加过引号、原文字节区间。
package cell

import "errors"

// 四类可区分的语法错误。
var (
	ErrBareQuote      = errors.New("bare quote in unquoted field")
	ErrQuoteExtra     = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field at end of input")
	ErrColumnCount    = errors.New("record field count mismatch")
	ErrBareCR         = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLarge  = errors.New("field exceeds max bytes")
	ErrTooManyFields  = errors.New("record exceeds max fields")
	ErrTooManyRecords = errors.New("table exceeds max records")
)

// Cell 是一个字段。Start/End 是原文中该字段占据的字节偏移区间 [Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Error 携带出错字节偏移（从 0 起）、记录号与字段号（从 1 起）。
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

// At 包装一个哨兵错误并带上定位信息。
func At(err error, offset, record, field int) *Error {
	return &Error{Err: err, Offset: offset, Record: record, Field: field}
}

// Limits 为三类可配置上限；0 表示用默认值。
type Limits struct {
	MaxFieldBytes  int
	MaxRecordFields int
	MaxRecords      int
}

// DefaultLimits：10MiB/字段、10000 字段/记录、1000000 记录。
var DefaultLimits = Limits{MaxFieldBytes: 10 << 20, MaxRecordFields: 10000, MaxRecords: 1000000}

func (l Limits) norm() Limits {
	if l.MaxFieldBytes <= 0 {
		l.MaxFieldBytes = DefaultLimits.MaxFieldBytes
	}
	if l.MaxRecordFields <= 0 {
		l.MaxRecordFields = DefaultLimits.MaxRecordFields
	}
	if l.MaxRecords <= 0 {
		l.MaxRecords = DefaultLimits.MaxRecords
	}
	return l
}

// Norm 补全零值上限为默认值。
func (l Limits) Norm() Limits { return l.norm() }
