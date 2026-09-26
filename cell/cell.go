// Package cell 表示 CSV 字段值：内容、引号标记与原文字节偏移。
package cell

import "fmt"

// Cell 是一个字段。Quoted 区分未引号空字段与 ""。
// Start/End 为原文半开区间 [Start,End) 的字节偏移。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// PosError 携带出错位置：字节偏移（从 0）、记录号、字段号（从 1）。
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

// 可判定的哨兵错误，四类语法错误彼此可区分。
var (
	ErrBareQuote     = fmt.Errorf("bare quote in unquoted field")
	ErrUnclosedQuote = fmt.Errorf("unclosed quoted field")
	ErrBareCR        = fmt.Errorf("bare carriage return")
	ErrColumnCount   = fmt.Errorf("record field count mismatch")
	ErrFieldTooLarge = fmt.Errorf("field exceeds max bytes")
	ErrTooManyFields = fmt.Errorf("record exceeds max fields")
	ErrTooManyRecords = fmt.Errorf("table exceeds max records")
)

// Limits 为三类可配置上限；零值表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}
