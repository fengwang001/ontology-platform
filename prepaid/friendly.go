package prepaid

import "math"

const secondsPerDay int64 = 86400

// inDailyWindow 判定日内秒 sec 是否落在每日友好时段内（左闭右开）。
// 起点等于终点表示不启用；终点小于起点表示跨午夜。
func (c Config) inDailyWindow(sec int64) bool {
	s, e := c.FriendlyStartSec, c.FriendlyEndSec
	if s == e {
		return false
	}
	if s < e {
		return sec >= s && sec < e
	}
	return sec >= s || sec < e
}

// weekdayOf 返回第 day 天对应的星期几（0..6）。
func (c Config) weekdayOf(day int64) int64 {
	return (day + c.EpochWeekday) % 7
}

// isFriendlyDay 判定某天是否全天友好（节假日或休息日）。
// 节假日存于哈希集合，查询开销与节假日总数无关。
func (a *Account) isFriendlyDay(day int64) bool {
	if a.holidays[day] {
		return true
	}
	return a.cfg.RestWeekdays[a.cfg.weekdayOf(day)]
}

// isFriendly 判定时刻 t 是否处于友好时段。
func (a *Account) isFriendly(t int64) bool {
	if a.isFriendlyDay(t / secondsPerDay) {
		return true
	}
	return a.cfg.inDailyWindow(t % secondsPerDay)
}

// windowEnd 返回第 day 天内容纳 sec 的每日友好时段的结束时刻。
// 调用前须保证 sec 在每日时段内且当天不是全天友好。
func (c Config) windowEnd(day, sec int64) int64 {
	s, e := c.FriendlyStartSec, c.FriendlyEndSec
	switch {
	case s < e:
		return day*secondsPerDay + e
	case sec >= s: // 跨午夜时段的前半段
		return (day+1)*secondsPerDay + e
	default: // 跨午夜时段的后半段
		return day*secondsPerDay + e
	}
}

// friendlyEnd 返回容纳 t 的友好时段的结束时刻，调用前须保证 isFriendly(t)。
// 友好时段为极大连续友好区间：每日时段结束若紧接全天友好日则顺延。
// 若每一天都是休息日，则友好时段无界，返回 math.MaxInt64。
func (a *Account) friendlyEnd(t int64) int64 {
	allRest := true
	for _, r := range a.cfg.RestWeekdays {
		if !r {
			allRest = false
			break
		}
	}
	if allRest {
		return math.MaxInt64
	}
	day, sec := t/secondsPerDay, t%secondsPerDay
	var end int64
	if a.isFriendlyDay(day) {
		end = (day + 1) * secondsPerDay
	} else {
		end = a.cfg.windowEnd(day, sec)
	}
	for {
		d, s := end/secondsPerDay, end%secondsPerDay
		if a.isFriendlyDay(d) {
			end = (d + 1) * secondsPerDay
			continue
		}
		if a.cfg.inDailyWindow(s) {
			end = a.cfg.windowEnd(d, s)
			continue
		}
		return end
	}
}

// deferCutoff 计算在时刻 t 触发停电的执行时刻：
// 若 t 处于友好时段则推迟到当前友好时段结束，否则为 t 本身。
func (a *Account) deferCutoff(t int64) int64 {
	if a.isFriendly(t) {
		return a.friendlyEnd(t)
	}
	return t
}
