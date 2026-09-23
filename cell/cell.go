// Package cell 表示 CSV 字段值：文本、引号标记与原文字节偏移。
package cell

import "fmt"

// Cell 是一个字段。Quoted 区分未引号空字段与 "" 空字段。
// Start/End 为原文字节偏移 [Start,End)；空字段指向其定界位置。
// 单列表中的空行记录被规范化为 Quoted=true（合成标记，保证往返，见 DESIGN.md 第1节）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// PosError 携带出错位置：字节偏移（从0起）、记录号与字段号（从1起）。
type PosError struct {
	Op     string
	Offset int
	Record int
	Field  int
	Err    error
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%s: %s at byte %d (record %d, field %d)", e.Op, e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Equal 逐字段比较（含引号标记与偏移）。
func (a Cell) Equal(b Cell) bool {
	return a.Value == b.Value && a.Quoted == b.Quoted && a.Start == b.Start && a.End == b.End
}
