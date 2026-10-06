package scheduler

import "math"

// calendar.go 负责时段网格与日历换算。
//
// 时段按日划分，每日含 slotsPerDay 个连续时段。时段编号在全日历范围内
// 唯一且单调：slot = day*slotsPerDay + indexInDay（均从 0 起）。

// Calendar 描述时段编号到「日 / 日内序号」的映射。
type Calendar struct {
	days        int
	slotsPerDay int
}

func newCalendar(cfg Config) *Calendar {
	return &Calendar{days: cfg.Days, slotsPerDay: cfg.SlotsPerDay}
}

func (c *Calendar) dayOf(slot int) int { return slot / c.slotsPerDay }

func (c *Calendar) indexInDay(slot int) int { return slot % c.slotsPerDay }

func (c *Calendar) valid(slot int) bool {
	return slot >= 0 && slot < c.days*c.slotsPerDay
}

// sameDay 判断两个时段是否属于同一日。
func (c *Calendar) sameDay(a, b int) bool { return c.dayOf(a) == c.dayOf(b) }

// checkRange 判断左闭右闭区间 [start, end] 是否：
//   - 起止均在日历范围内；
//   - 至少包含一个时段（end >= start）；
//   - 不跨日。
//
// 满足返回 (day, true)，否则返回 (0, false)。
func (c *Calendar) checkRange(start, end int) (int, bool) {
	if !c.valid(start) || !c.valid(end) || end < start {
		return 0, false
	}
	if c.dayOf(start) != c.dayOf(end) {
		return 0, false
	}
	return c.dayOf(start), true
}

func (c *Calendar) totalSlots() int { return c.days * c.slotsPerDay }

// ceilSlots 将“标准时段数 * 比例”向上取整为整时段数。
// 换算结果不足一个时段的部分向上进位。
func ceilSlots(standard int, ratio float64) int {
	exact := float64(standard) * ratio
	return int(math.Ceil(exact - 1e-9))
}
