// Package calendar 负责本地日界、节假日与宵禁判定。
package calendar

import "errors"

var (
	ErrInvalidParam = errors.New("calendar: invalid parameter")
	ErrTooLate      = errors.New("calendar: day already started")
)

// Calendar 保存时区偏移、宵禁区间与节假日表。
type Calendar struct {
	tz int64
	cs int64
	ce int64

	holidays map[int64]bool
}

// New 创建日历。tz/cs/ce 单位为秒。
// 参数越界视为编程错误（构造期非法），直接 panic。
func New(tz, cs, ce int64) *Calendar {
	if tz < -43200 || tz > 50400 || cs < 0 || cs > 86399 || ce < 0 || ce > 86399 {
		panic(ErrInvalidParam)
	}
	return &Calendar{tz: tz, cs: cs, ce: ce, holidays: map[int64]bool{}}
}

// Day 返回 t 对应的本地日号。
func (c *Calendar) Day(t int64) int64 {
	day, _ := c.Local(t)
	return day
}

// Local 返回 t 的本地日号与日内秒。
func (c *Calendar) Local(t int64) (day, x int64) {
	s := t + c.tz
	day = floorDiv(s, 86400)
	x = s - day*86400
	return day, x
}

// IsHoliday 报告某日是否被设为节假日。
func (c *Calendar) IsHoliday(day int64) bool { return c.holidays[day] }

// Curfew 报告日内秒 x 是否处于宵禁中。
func (c *Calendar) Curfew(x int64) bool {
	switch {
	case c.cs == c.ce:
		return false
	case c.cs < c.ce:
		return c.cs <= x && x < c.ce
	default:
		return x >= c.cs || x < c.ce
	}
}

// InCurfew 报告时刻 t 是否处于宵禁中。
func (c *Calendar) InCurfew(t int64) bool {
	_, x := c.Local(t)
	return c.Curfew(x)
}

// SetHoliday 设置/取消节假日；只能改尚未开始的日。
func (c *Calendar) SetHoliday(now, day int64, on bool) error {
	if now < 0 || now > 1e10 {
		return ErrInvalidParam
	}
	today := c.Day(now)
	if day <= today {
		return ErrTooLate
	}
	if on {
		c.holidays[day] = true
	} else {
		delete(c.holidays, day)
	}
	return nil
}

// NextCurfewStart 返回 >=from 的下一个进入宵禁的时刻；无宵禁返回 -1。
func (c *Calendar) NextCurfewStart(from int64) int64 {
	if c.cs == c.ce {
		return -1
	}
	day, _ := c.Local(from)
	for d := day; d <= day+1; d++ {
		start := c.DayStart(d) + c.cs
		if start >= from {
			return start
		}
	}
	return -1
}

// DayStart 返回本地日 day 的起始 UTC 时刻：day*86400 - tz。
func (c *Calendar) DayStart(day int64) int64 { return day*86400 - c.tz }

// floorDiv 返回数学向下取整的整数除法（分母为正）。
func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
