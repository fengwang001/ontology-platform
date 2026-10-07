package approval

import "sort"

// calendar 工作日历：以整数日序号为时间轴，非工作日集合在初始化时给定。
// 所有时限均按工作日计量，计数与定位均为 O(log n)。
type calendar struct {
	nonWork map[int]struct{}
	sorted  []int // 非工作日升序、去重
}

func newCalendar(days []int) (*calendar, error) {
	c := &calendar{nonWork: make(map[int]struct{}, len(days))}
	for _, d := range days {
		if d < 0 {
			return nil, invalidf("非工作日序号不能为负: %d", d)
		}
		if _, ok := c.nonWork[d]; !ok {
			c.nonWork[d] = struct{}{}
			c.sorted = append(c.sorted, d)
		}
	}
	sort.Ints(c.sorted)
	return c, nil
}

func (c *calendar) isWorkday(d int) bool {
	_, ok := c.nonWork[d]
	return !ok
}

// nonWorkBetween 统计 (from, to] 区间内的非工作日数，要求 to >= from。
func (c *calendar) nonWorkBetween(from, to int) int {
	lo := sort.SearchInts(c.sorted, from+1)
	hi := sort.SearchInts(c.sorted, to+1)
	return hi - lo
}

// workdaysBetween 统计 (from, to] 区间内的工作日数，要求 to >= from。
func (c *calendar) workdaysBetween(from, to int) int {
	if to <= from {
		return 0
	}
	return to - from - c.nonWorkBetween(from, to)
}

// addWorkdays 返回 t 之后（不含 t）第 n 个工作日的日序号；n <= 0 时返回 t 本身。
func (c *calendar) addWorkdays(t, n int) int {
	if n <= 0 {
		return t
	}
	d := t + n
	for {
		nd := t + n + c.nonWorkBetween(t, d)
		if nd == d {
			return d
		}
		d = nd
	}
}

// nextWorkday 返回 t 之后（不含 t）的第一个工作日。
func (c *calendar) nextWorkday(t int) int {
	return c.addWorkdays(t, 1)
}
