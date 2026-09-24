package cell

import (
	"errors"
	"fmt"
)

// Cell 表示一个字段的解析结果。
// Start/End 是字段在原文中的字节偏移区间 [Start,End)，
// 引号字段包含外围两个引号；Value 是反转义后的内容（\r\n 原样保留）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// 可判定的哨兵错误：四类语法错误 + 三类上限。
var (
	ErrBadQuote      = errors.New("unexpected '\"' in unquoted field")
	ErrAfterQuote    = errors.New("unexpected character after closing quote")
	ErrUnterminated  = errors.New("unterminated quoted field at end of input")
	ErrBareCR        = errors.New("bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge = errors.New("field exceeds max bytes")
	ErrTooManyFields = errors.New("record exceeds max fields")
	ErrTooManyRows   = errors.New("table exceeds max records")
	ErrFieldCount    = errors.New("record field count differs from header")
)

// PosError 为错误附带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}

func (e *PosError) Unwrap() error { return e.Err }

// At 构造带位置的错误。
func At(err error, offset, record, field int) error {
	return &PosError{Err: err, Offset: offset, Record: record, Field: field}
}

// OffsetOf/RecordOf/FieldOf 供调用方判定错误位置。
func Pos(err error) (offset, record, field int, ok bool) {
	var pe *PosError
	if errors.As(err, &pe) {
		return pe.Offset, pe.Record, pe.Field, true
	}
	return 0, 0, 0, false
}
