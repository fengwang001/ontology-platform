package calendar

import "errors"

var (
	ErrInvalidParam = errors.New("calendar: invalid parameter")
	ErrTooLate      = errors.New("calendar: holiday set too late")
)

type Cal struct {
	tz, cs, ce int64
	holiday    map[int64]bool
}

// New 构造本地日历。tz 为时区偏移秒；cs、ce 为宵禁起止的日内秒。
func New(tz, cs, ce int64) (*Cal, error) {
	if tz < -43200 || tz > 50400 || cs < 0 || cs > 86399 || ce < 0 || ce > 86399 {
		return nil, ErrInvalidParam
	}
	return &Cal{tz: tz, cs: cs, ce: ce, holiday: map[int64]bool{}}, nil
}

// Day 返回本地日号 floor((t+tz)/86400)，对负数做数学向下取整。
func (c *Cal) Day(t int64) int64 {
	q, _ := floorDivMod(t+c.tz, 86400)
	return q
}

// SecOfDay 返回 t 在本地日中的日内秒，范围 0..86399。
func (c *Cal) SecOfDay(t int64) int64 {
	_, r := floorDivMod(t+c.tz, 86400)
	return r
}

// DayStart 返回本地日 day 起始的绝对秒 day*86400-tz。
func (c *Cal) DayStart(day int64) int64 { return day*86400 - c.tz }

// CurfewAt 判定 t 是否处于宵禁时段（取等进入、取等退出）。
func (c *Cal) CurfewAt(t int64) bool {
	if c.cs == c.ce {
		return false
	}
	x := c.SecOfDay(t)
	if c.cs < c.ce {
		return x >= c.cs && x < c.ce
	}
	return x >= c.cs || x < c.ce
}

// NextCurfewStart 返回严格晚于 t 的下一个宵禁开始时刻；无宵禁返回 ok=false。
func (c *Cal) NextCurfewStart(t int64) (int64, bool) {
	if c.cs == c.ce {
		return 0, false
	}
	d := c.Day(t)
	s := c.DayStart(d) + c.cs
	if s <= t {
		s += 86400
	}
	return s, true
}

func (c *Cal) IsHoliday(day int64) bool { return c.holiday[day] }

// SetHoliday 设置节假日标记；日已开始（day 起始时刻 <= now）则为时已晚。
func (c *Cal) SetHoliday(now, day int64, on bool) error {
	if now < 0 {
		return ErrInvalidParam
	}
	if c.DayStart(day) <= now {
		return ErrTooLate
	}
	if on {
		c.holiday[day] = true
	} else {
		delete(c.holiday, day)
	}
	return nil
}

func floorDivMod(a, b int64) (q, r int64) {
	q = a / b
	r = a % b
	if r < 0 {
		q--
		r += b
	}
	return q, r
}
