package permit

// Calendar 工作日历法：整数日序号 -> 是否工作日。
// 非工作日集合在初始化时给定，工作日判断无副作用。
type Calendar struct {
	nonWorking map[int]struct{}
}

// NewCalendar 以非工作日集合构造历法。
func NewCalendar(nonWorking []int) *Calendar {
	c := &Calendar{nonWorking: make(map[int]struct{}, len(nonWorking))}
	for _, d := range nonWorking {
		c.nonWorking[d] = struct{}{}
	}
	return c
}

// IsWorking 报告 day 是否为工作日。
func (c *Calendar) IsWorking(day int) bool {
	_, off := c.nonWorking[day]
	return !off
}

// NextWorking 返回 day 当日（含）起的第一个工作日。
func (c *Calendar) NextWorking(day int) int {
	for !c.IsWorking(day) {
		day++
	}
	return day
}

// NextWorkingAfter 返回 day 之后（不含）的第一个工作日。
func (c *Calendar) NextWorkingAfter(day int) int {
	return c.NextWorking(day + 1)
}

// PrevWorking 返回 day 当日（含）起向前的第一个工作日。
func (c *Calendar) PrevWorking(day int) int {
	for !c.IsWorking(day) {
		day--
	}
	return day
}

// AddWorking 返回从 from 的次日起数 n 个工作日所到的工作日。
// 即“起算日的下一工作日”为第 1 个的计数模型。
func (c *Calendar) AddWorking(from int, n int) int {
	if n <= 0 {
		return c.PrevWorking(from)
	}
	// 起算日 = from 的下一工作日，它是第 1 个，故从 from+1（含）起数 n 个。
	return c.NthWorkingOnOrAfter(from+1, n)
}

// WorkingBetween 返回区间 (from, to] 内的工作日数。
func (c *Calendar) WorkingBetween(from, to int) int {
	if to <= from {
		return 0
	}
	n := 0
	for d := from + 1; d <= to; d++ {
		if c.IsWorking(d) {
			n++
		}
	}
	return n
}

// WorkingIn 返回闭区间 [from, to] 内的工作日数。
func (c *Calendar) WorkingIn(from, to int) int {
	if to < from {
		return 0
	}
	n := 0
	for d := from; d <= to; d++ {
		if c.IsWorking(d) {
			n++
		}
	}
	return n
}

// NthWorkingOnOrAfter 返回 from 当日（含）起的第 n 个工作日（n>=1）。
func (c *Calendar) NthWorkingOnOrAfter(from int, n int) int {
	if n <= 0 {
		return c.PrevWorking(from)
	}
	d := from
	for {
		if c.IsWorking(d) {
			n--
			if n == 0 {
				return d
			}
		}
		d++
	}
}
