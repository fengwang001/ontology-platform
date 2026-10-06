package policy

// FullSurrender 整单退保，按当前时刻计算。
// 犹豫期内退还全部实缴保费减工本费（不足为零）；犹豫期后退还现金价值。
// 存在预约或待补缴批改时拒绝。退保后保单终止。
func (e *Engine) FullSurrender(policyID string) (SurrenderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" {
		return SurrenderResult{}, ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return SurrenderResult{}, ErrPolicyMissing
	}
	if p.terminated {
		return SurrenderResult{}, ErrTerminated
	}
	if err := p.blockedByPending(); err != nil {
		return SurrenderResult{}, err
	}
	var payout int64
	if p.inCooling(e.now) {
		payout = p.paidTotal - p.cfg.PolicyFee
		if payout < 0 {
			payout = 0
		}
	} else {
		payout = p.cashValue(e.now, p.loan)
	}
	p.terminated = true
	return SurrenderResult{
		Payout:        payout,
		CashValue:     payout,
		AnnualPremium: p.annualPremium,
		SumAssured:    p.sumAssured,
	}, nil
}

// PartialSurrender 部分退保：reduceSA 为减少的保额（正整数分）。
// 仅犹豫期后允许；按减少保额占比退还现金价值的同比例部分（向下取整），
// 年缴保费按剩余保额占比向上取整，
// 退保后保额不得低于最低保额。
func (e *Engine) PartialSurrender(policyID string, reduceSA int64) (SurrenderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || reduceSA <= 0 {
		return SurrenderResult{}, ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return SurrenderResult{}, ErrPolicyMissing
	}
	if p.terminated {
		return SurrenderResult{}, ErrTerminated
	}
	if p.inCooling(e.now) {
		return SurrenderResult{}, ErrInvalid
	}
	newSA := p.sumAssured - reduceSA
	if newSA < p.cfg.MinSumAssured {
		return SurrenderResult{}, ErrBelowMinSA
	}
	oldSA := p.sumAssured
	cv := p.cashValue(e.now, p.loan)
	payout := cv * reduceSA / oldSA
	// 剩余实缴保费按剩余保额占比向下取整，使保留部分的现金价值
	// 精确等于 cv-payout（payout 同为向下取整）。
	p.paidTotal = p.paidTotal * newSA / oldSA
	p.annualPremium = ceilDiv(p.annualPremium*newSA, oldSA)
	p.sumAssured = newSA
	return SurrenderResult{
		Payout:        payout,
		CashValue:     cv,
		AnnualPremium: p.annualPremium,
		SumAssured:    p.sumAssured,
	}, nil
}

// blockedByPending 检查退保阻碍：待补缴优先于仅有预约。
func (p *policy) blockedByPending() error {
	for _, en := range p.pending {
		if en.status == StatusAwaitingPay {
			return ErrAwaitingPay
		}
	}
	if len(p.pending) > 0 {
		return ErrPendingEnds
	}
	return nil
}
