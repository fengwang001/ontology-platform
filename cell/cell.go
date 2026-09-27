// Package cell 表示 CSV 字段值及其原文引号标记与字节偏移。
package cell

import "fmt"

// Cell 是一个字段。Quoted 区分未引号空字段与加引号空字段 ("")。
// Start/End 是该字段在原文中的字节偏移区间 [Start,End)；
// 引号字段 Start 指向开引号，End 指向闭引号之后。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// PosError 携带出错位置：字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("csv: %v at offset %d record %d field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// NewPosError 包装哨兵错误并附位置。
func NewPosError(err error, offset, record, field int) *PosError {
	return &PosError{Err: err, Offset: offset, Record: record, Field: field}
}
