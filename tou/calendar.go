package tou

import "time"

// calendar 维护节假日登记，结合自然周末判定日类型。
// 节假日优先于休息日，休息日优先于工作日。
type calendar struct {
	holidays map[string]bool
}

func newCalendar() *calendar {
	return &calendar{holidays: map[string]bool{}}
}

func dateKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// ParseDate 解析 "YYYY-MM-DD"（按 loc 解释）。格式错误属于参数非法。
func ParseDate(s string, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	if !validMoment(t) {
		return time.Time{}, false
	}
	return t, true
}

func (c *calendar) setHoliday(day time.Time, on bool) {
	k := dateKey(day)
	if on {
		c.holidays[k] = true
	} else {
		delete(c.holidays, k)
	}
}

// dayType 只决定使用哪张时段表，不改变时段边界的时刻值。
func (c *calendar) dayType(t time.Time) DayType {
	if c.holidays[dateKey(t)] {
		return DayHoliday
	}
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		return DayWeekend
	default:
		return DayWorkday
	}
}
