// Package bizday 提供工作日历能力：工作日判定、工作日加减与区间计数。
//
// 日期一律使用 YYYYMMDD 形式的 int 表示（例如 20260921）。
// 本包不依赖 time 包，闰年、月份天数与星期几均自行计算。
package bizday

import "errors"

// 哨兵错误，调用方可用 errors.Is 判定。
var (
	// ErrInvalidDate 表示日期非法：年份超出 [1900, 2999]、
	// 月份不在 1-12，或日超出该月实际天数。
	ErrInvalidDate = errors.New("bizday: invalid date")

	// ErrInvalidRange 表示 CountBusinessDays 收到 from > to 的区间。
	ErrInvalidRange = errors.New("bizday: invalid range, from > to")

	// ErrNotBusinessDay 表示 AddBusinessDays(d, 0) 中的 d 不是工作日。
	ErrNotBusinessDay = errors.New("bizday: not a business day")
)
