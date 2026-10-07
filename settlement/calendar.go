package settlement

import "sort"

// Calendar 营业日集合（整数天，升序去重后不可变）。
// 日历在引擎生命周期内固定，必须覆盖全部结算与保证金到期区间。
type Calendar struct {
	days []int64
}

// NewCalendar 由任意顺序的整数天构造营业日日历（去重、排序）。
func NewCalendar(days []int64) Calendar {
	cp := make([]int64, len(days))
	copy(cp, days)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	out := cp[:0]
	for i, d := range cp {
		if i == 0 || d != cp[i-1] {
			out = append(out, d)
		}
	}
	return Calendar{days: out}
}

// IsBusinessDay 判断 d 是否为营业日。
func (c Calendar) IsBusinessDay(d int64) bool {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] >= d })
	return i < len(c.days) && c.days[i] == d
}

// Retreat 返回 d 之前（严格早于）第 n 个营业日；不存在时 ok=false。
// 即"t 往前数第 N 个营业日"：n=1 为 d 之前最近的一个营业日。
func (c Calendar) Retreat(d int64, n int) (day int64, ok bool) {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] >= d })
	// days[0..i-1] 均严格小于 d，第 n 个为下标 i-n。
	if i-n < 0 {
		return 0, false
	}
	return c.days[i-n], true
}

// Advance 返回 d 之后（严格晚于）第 n 个营业日；不存在时 ok=false。
// 即"留存日之后第 H 个营业日"：n=1 为 d 之后最近的一个营业日。
func (c Calendar) Advance(d int64, n int) (day int64, ok bool) {
	i := sort.Search(len(c.days), func(i int) bool { return c.days[i] > d })
	// days[i..] 均严格大于 d，第 n 个为下标 i+n-1。
	if i+n-1 >= len(c.days) {
		return 0, false
	}
	return c.days[i+n-1], true
}

// DaysBetween 返回 (exclusiveLo, inclusiveHi] 区间内升序的营业日。
func (c Calendar) DaysBetween(exclusiveLo, inclusiveHi int64) []int64 {
	lo := sort.Search(len(c.days), func(i int) bool { return c.days[i] > exclusiveLo })
	hi := sort.Search(len(c.days), func(i int) bool { return c.days[i] > inclusiveHi })
	if lo >= hi {
		return nil
	}
	out := make([]int64, hi-lo)
	copy(out, c.days[lo:hi])
	return out
}
