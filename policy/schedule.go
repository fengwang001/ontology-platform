package policy

// paymentSchedule 缴费计划：由生效日、缴费周期与已缴期数纯函数式推导应缴日，
// 不保存任何逐期历史，因此查询开销与已经历的缴费期数无关。
//
// 第 n 期应缴日 = 生效日 + (n-1) 个周期；首期在登记时视为已缴。
type paymentSchedule struct {
	effectiveDay int64
	periodDays   int64
	paid         int64 // 已缴期数
}

// nextDue 下一期应缴日（第 paid+1 期）。
func (s paymentSchedule) nextDue() int64 {
	return s.effectiveDay + s.paid*s.periodDays
}

// duePeriods 截至 day（含当天）已到期而未缴的期数。
func (s paymentSchedule) duePeriods(day int64) int64 {
	nd := s.nextDue()
	if day < nd {
		return 0
	}
	return (day-nd)/s.periodDays + 1
}
