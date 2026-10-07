package bitemporal

import "time"

// Empty 报告区间是否为空（From 与 To 相等，或 From 晚于 To）。
func (i Interval) Empty() bool {
	// From == To 为空区间；From > To 视为非法/空区间，二者都不可能覆盖任何时间点。
	return !i.From.Before(i.To)
}

// Contains 按左闭右开语义报告 t 是否落在区间内。
func (i Interval) Contains(t time.Time) bool {
	if i.Empty() {
		return false
	}
	return !t.Before(i.From) && t.Before(i.To)
}
