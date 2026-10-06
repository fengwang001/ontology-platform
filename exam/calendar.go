package exam

import "fmt"

// Calendar 描述考试时段网格：按日划分，每日含若干连续时段。
// 全局时段编号 slot = day*slotsPerDay + idx，在全日历内唯一且单调。
type Calendar struct {
	days        int
	slotsPerDay int
}

// NewCalendar 构造日历；days 为天数，slotsPerDay 为每日连续时段数。
func NewCalendar(days, slotsPerDay int) (*Calendar, error) {
	if days <= 0 || slotsPerDay <= 0 {
		return nil, fmt.Errorf("invalid calendar: days=%d slotsPerDay=%d", days, slotsPerDay)
	}
	return &Calendar{days: days, slotsPerDay: slotsPerDay}, nil
}

// Days 返回天数。
func (c *Calendar) Days() int { return c.days }

// SlotsPerDay 返回每日时段数。
func (c *Calendar) SlotsPerDay() int { return c.slotsPerDay }

// SlotID 返回第 day 日（0 基）内第 idx 时段（0 基）的全局唯一单调编号。
func (c *Calendar) SlotID(day, idx int) int {
	if day < 0 || day >= c.days || idx < 0 || idx >= c.slotsPerDay {
		return -1
	}
	return day*c.slotsPerDay + idx
}

// DayOf 返回某全局时段编号所在日（0 基）；非法返回 -1。
func (c *Calendar) DayOf(slot int) int {
	if !c.InRange(slot) {
		return -1
	}
	return slot / c.slotsPerDay
}

// IndexInDay 返回某全局时段在当日内的序号（0 基）；非法返回 -1。
func (c *Calendar) IndexInDay(slot int) int {
	if !c.InRange(slot) {
		return -1
	}
	return slot % c.slotsPerDay
}

// InRange 判断全局时段编号是否在日历范围内。
func (c *Calendar) InRange(slot int) bool {
	return slot >= 0 && slot < c.days*c.slotsPerDay
}

// SameDay 判断两个全局时段是否属于同一日；任一非法返回 false。
func (c *Calendar) SameDay(a, b int) bool {
	if !c.InRange(a) || !c.InRange(b) {
		return false
	}
	return a/c.slotsPerDay == b/c.slotsPerDay
}

// DaySlots 返回某日的全部时段编号，升序；非法日返回 nil。
func (c *Calendar) DaySlots(day int) []int {
	if day < 0 || day >= c.days {
		return nil
	}
	out := make([]int, c.slotsPerDay)
	base := day * c.slotsPerDay
	for i := range out {
		out[i] = base + i
	}
	return out
}
