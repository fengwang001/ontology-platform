// Package cell 表示 CSV 字段值：内容、引号标记与原文字节偏移。
package cell

import (
	"errors"
	"fmt"
)

// Cell 是一个字段的解析结果。Start/End 为原文中的字节偏移（End 为开区间，
// 指向字段定界符或其后位置）；Quoted 区分未加引号空字段与 ""。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Record 是一条记录；Table 是记录切片。
type Record []Cell

// Table 是记录切片。
type Table []Record

// 四类语法错误与三类上限错误的哨兵，均可用 errors.Is 判定。
var (
	ErrBareQuote         = errors.New("csv: quote in unquoted field")
	ErrQuoteFollow       = errors.New("csv: non-delimiter after closing quote")
	ErrUnterminatedQuote = errors.New("csv: unterminated quoted field")
	ErrDanglingCR        = errors.New("csv: bare carriage return")
	ErrFieldCount        = errors.New("csv: inconsistent field count")
	ErrTooManyFields     = errors.New("csv: too many fields in record")
	ErrFieldTooLarge     = errors.New("csv: field too large")
	ErrTooManyRecords    = errors.New("csv: too many records")
)

// Error 带位置的错误：Offset 为字节偏移（0 起），Record/Field 从 1 起。
type Error struct {
	Kind   error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at offset %d (record %d, field %d)", e.Kind, e.Offset, e.Record, e.Field)
}

func (e *Error) Unwrap() error { return e.Kind }

// Is 使哨兵判定穿透 *Error。
func (e *Error) Is(target error) bool { return e.Kind == target }
