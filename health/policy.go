package health

// policy 一张已登记保单。
type policy struct {
	in         PolicyInput
	renewal    bool  // 是否为连续续保
	baseAmount int64 // 续保前原保额（提高保额时用于新等待期上限）
}

// covers 覆盖区间为左端包含、右端不包含：[Start, End)。
// 因此到期日当天出险不在本保单覆盖区间。
func (p *policy) covers(day int64) bool {
	return day >= p.in.Start && day < p.in.End
}

// waitingLastDay 等待期自生效日起连续 WaitDays 天、生效日算第 1 天，
// 故等待期闭区间为 [Start, Start+WaitDays-1]，本方法返回右端点；
// WaitDays 为 0（连续续保未提高保额）时返回 Start-1，表示无等待期。
func (p *policy) waitingLastDay() int64 {
	return p.in.Start + p.in.WaitDays - 1
}

// inWaiting 判断出险日是否落在等待期内。
func (p *policy) inWaiting(day int64) bool {
	return p.in.WaitDays > 0 && day <= p.waitingLastDay()
}

// overlaps 判断两张保单覆盖区间是否重叠（相接 [a,b)[b,c) 不重叠）。
func (p *policy) overlaps(q *policy) bool {
	return p.in.Start < q.in.End && q.in.Start < p.in.End
}

// renewablePrev 判断 old 是否为 new 的连续续保前序保单：
// 新生效日恰等于原到期日，且在原到期日之前（含当天）登记。
func renewablePrev(old *policy, newStart, registeredAt int64) bool {
	return newStart == old.in.End && registeredAt <= old.in.End
}

// capFor 返回某诊断在本保单下、出险日的适用保额上限：
// 提高保额的连续续保，等待期内非意外诊断以原保额为上限；
// 意外诊断不受等待期约束，始终按完整保额。
func (p *policy) capFor(day int64, accident bool) int64 {
	if p.renewal && !accident && p.inWaiting(day) {
		return p.baseAmount
	}
	return p.in.Amount
}
