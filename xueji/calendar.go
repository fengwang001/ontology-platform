package xueji

// Semester 学期，区间为左闭右开 [Start, End)；Deadline 为该学期异动
// 配置截止时刻，提交时刻恰等于 Deadline 视为"之前"。
type Semester struct {
	Start    int64
	End      int64
	Deadline int64
}

// Calendar 学期日历：有序、互不重叠的学期序列。
type Calendar struct {
	sems []Semester
}

// NewCalendar 校验并构建日历：学期按起始时刻严格递增、互不重叠，
// 且满足 Start <= Deadline < End。
func NewCalendar(sems []Semester) (*Calendar, error) {
	if len(sems) == 0 {
		return nil, errf(ErrInvalidParam, "学期日历为空")
	}
	for i, s := range sems {
		if !(s.Start < s.End && s.Start <= s.Deadline && s.Deadline < s.End) {
			return nil, errf(ErrInvalidParam, "学期 %d 区间非法: %+v", i, s)
		}
		if i > 0 && sems[i-1].End > s.Start {
			return nil, errf(ErrInvalidParam, "学期 %d 与前一学期重叠", i)
		}
	}
	return &Calendar{sems: append([]Semester(nil), sems...)}, nil
}

// At 返回时刻 t 所在学期的下标；不在任何学期内返回 -1。二分查找，
// 开销与学期总数对数相关，与学生总数无关。
func (c *Calendar) At(t int64) int {
	lo, hi := 0, len(c.sems)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case t < c.sems[mid].Start:
			hi = mid
		case t >= c.sems[mid].End:
			lo = mid + 1
		default:
			return mid
		}
	}
	return -1
}

// Start 返回第 i 学期的起始时刻。
func (c *Calendar) Start(i int) int64 { return c.sems[i].Start }

// Deadline 返回第 i 学期的截止时刻。
func (c *Calendar) Deadline(i int) int64 { return c.sems[i].Deadline }

// Count 返回学期总数。
func (c *Calendar) Count() int { return len(c.sems) }
