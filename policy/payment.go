package policy

// PayPremium 以保单年度为单位缴费，一次恰好一年当前年缴保费，
// 只能缴当前年度或下一年度，已缴年度重复缴费报“已缴”。
func (e *Engine) PayPremium(id string, year, amount int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if year < 0 || amount <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[id]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	cur := p.yearOf(p.now)
	if (year != cur && year != cur+1) || amount != p.premium {
		return newErr(ErrInvalidParam, "参数非法")
	}
	if p.paidYears[year] {
		return newErr(ErrAlreadyPaid, "已缴")
	}
	p.paidYears[year] = true
	p.totalPaid += amount
	return nil
}
