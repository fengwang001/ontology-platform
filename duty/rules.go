package duty

// Period 返回时刻 t 在一天中所属时段（0=早，1=日，2=夜）。
// 时段边界左闭右开：[0,b1)、[b1,b2)、[b2,DayLen)。
func (c Config) Period(t int) int {
	m := t % c.DayLen
	switch {
	case m < c.DayBoundary1:
		return 0
	case m < c.DayBoundary2:
		return 1
	default:
		return 2
	}
}

// SingleLimit 返回报到时刻 start、航段数 legs 对应的单次值勤上限。
// 每多一个航段在上限基础上递减 PerLegCut，但不低于 MinLimit。
func (c Config) SingleLimit(start, legs int) int {
	l := c.BaseLimit[c.Period(start)] - legs*c.PerLegCut
	if l < c.MinLimit {
		l = c.MinLimit
	}
	return l
}

// RestRequired 返回紧随时长 prevLen 的值勤期之后所需的最小休息。
func (c Config) RestRequired(prevLen int) int {
	if c.MinRest > prevLen {
		return c.MinRest
	}
	return prevLen
}

// QualValid 报告到期时刻 expiry 对解除时刻 end 是否有效：必须严格大于。
func QualValid(expiry, end int, held bool) bool {
	return held && expiry > end
}
