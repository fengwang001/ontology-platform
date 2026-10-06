package policy

// PayPremium 缴纳恰好一个保单年度的保费；year 为保单年度序号。
// 只能缴当前年度或下一年度；已缴年度重复缴费报“已缴”。
func (e *Engine) PayPremium(policyID string, year int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || year <= 0 {
		return ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return ErrPolicyMissing
	}
	if p.terminated {
		return ErrTerminated
	}
	current := policyYear(e.now, p.cfg.EffectiveDay)
	if year != current && year != current+1 {
		return ErrInvalid
	}
	if _, paid := p.paidYears[year]; paid {
		return ErrAlreadyPaid
	}
	p.paidYears[year] = struct{}{}
	p.paidTotal += p.annualPremium
	return nil
}
