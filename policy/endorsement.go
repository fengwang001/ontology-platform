package policy

// Schedule 预约批改。
func (e *Engine) Schedule(req EndRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !req.valid() {
		return ErrInvalid
	}
	p, ok := e.policies[req.PolicyID]
	if !ok {
		return ErrPolicyMissing
	}
	if p.terminated {
		return ErrTerminated
	}
	if _, dup := p.endByID[req.EndID]; dup {
		return ErrEndDuplicate
	}
	// 生效日不得早于申请日、当前时刻、已生效批改最晚生效日。
	if req.EffectiveDay < req.ApplyDay || req.EffectiveDay < e.now ||
		req.EffectiveDay < p.lastEffectiveEndDay {
		return ErrRetroactive
	}
	en := &endorsement{
		kind:           req.Kind,
		policyID:       req.PolicyID,
		endID:          req.EndID,
		applyDay:       req.ApplyDay,
		effectiveDay:   req.EffectiveDay,
		seq:            e.nextSeq(),
		status:         StatusScheduled,
		newSumAssured:  req.NewSumAssured,
		newPayPeriods:  req.NewPayPeriods,
		newBeneficiary: req.NewBeneficiary,
	}
	p.endByID[en.endID] = en
	p.pending[en.endID] = en
	p.heap.push(en)
	// 生效日恰为当前时刻：立即尝试生效（缺补缴则停留待补缴）。
	if en.effectiveDay <= e.now {
		p.heap.remove(en.endID)
		e.applyEnd(p, en)
	}
	return nil
}

// Cancel 撤销预约/待补缴状态的批改；已生效报“已生效”。
func (e *Engine) Cancel(policyID, endID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || endID == "" {
		return ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return ErrPolicyMissing
	}
	if p.terminated {
		return ErrTerminated
	}
	en, ok := p.endByID[endID]
	if !ok {
		return ErrEndMissing
	}
	if en.status == StatusEffective {
		return ErrEndEffective
	}
	if en.status == StatusCancelled {
		return ErrEndEffective
	}
	// 预约或待补缴均可撤销；待补缴时尚未收款，故无款项需要回退。
	p.heap.remove(en.endID)
	delete(p.pending, en.endID)
	en.status = StatusCancelled
	return nil
}

// PaySurcharge 补缴到账；金额必须恰好等于应补金额，批改随后立即生效。
func (e *Engine) PaySurcharge(policyID, endID string, amount int64) (EndInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || endID == "" || amount <= 0 {
		return EndInfo{}, ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return EndInfo{}, ErrPolicyMissing
	}
	if p.terminated {
		return EndInfo{}, ErrTerminated
	}
	en, ok := p.endByID[endID]
	if !ok {
		return EndInfo{}, ErrEndMissing
	}
	if en.status != StatusAwaitingPay {
		if en.status == StatusEffective {
			return EndInfo{}, ErrEndEffective
		}
		return EndInfo{}, ErrInvalid
	}
	if amount != en.surcharge {
		return EndInfo{}, ErrInvalid
	}
	e.finishApply(p, en)
	return en.info(), nil
}

// applyEnd 在调用方持锁且批改已到生效日时执行生效账务。
// 保额增加且需要补缴时转入待补缴；其余情形立即完成生效。
func (e *Engine) applyEnd(p *policy, en *endorsement) {
	en.surcharge, en.refund = e.premiumDelta(p, en)
	if en.surcharge > 0 {
		en.status = StatusAwaitingPay
		return
	}
	e.finishApply(p, en)
}

// finishApply 落地批改：更新保额/保费/受益人/缴费期并结算补缴或退还。
func (e *Engine) finishApply(p *policy, en *endorsement) {
	switch en.kind {
	case KindAmountChange:
		if en.surcharge > 0 {
			p.paidTotal += en.surcharge
		}
		if en.refund > 0 {
			p.paidTotal -= en.refund
		}
		oldSA := p.sumAssured
		p.annualPremium = ceilDiv(p.annualPremium*en.newSumAssured, oldSA)
		p.sumAssured = en.newSumAssured
	case KindPayPeriodChange:
		p.payPeriods = en.newPayPeriods
	case KindBeneficiaryChange:
		p.beneficiary = en.newBeneficiary
	}
	en.status = StatusEffective
	delete(p.pending, en.endID)
	p.heap.remove(en.endID)
	if en.effectiveDay > p.lastEffectiveEndDay {
		p.lastEffectiveEndDay = en.effectiveDay
	}
}

// premiumDelta 计算保额变更在生效日所在保单年度的补缴/退还。
// 新保费 = ceil(旧年缴保费 * 新保额 / 旧保额)。
// 仅当本年度已缴时，按本年度剩余天数占 365 的比例结算已缴部分：
// 增加补缴向上取整，减少退还向下取整。
func (e *Engine) premiumDelta(p *policy, en *endorsement) (surcharge, refund int64) {
	if en.kind != KindAmountChange || en.newSumAssured == p.sumAssured {
		return 0, 0
	}
	year := policyYear(en.effectiveDay, p.cfg.EffectiveDay)
	if _, paid := p.paidYears[year]; !paid {
		return 0, 0
	}
	yearStart := p.cfg.EffectiveDay + (year-1)*365
	remaining := yearStart + 365 - en.effectiveDay // 左闭右开，生效日当天仍计为剩余
	newPremium := ceilDiv(p.annualPremium*en.newSumAssured, p.sumAssured)
	diff := newPremium - p.annualPremium
	switch {
	case diff > 0:
		return ceilDiv(diff*remaining, 365), 0
	case diff < 0:
		return 0, (-diff) * remaining / 365
	}
	return 0, 0
}

func (e *Engine) nextSeq() int64 {
	e.seq++
	return e.seq
}

func (r EndRequest) valid() bool {
	if r.PolicyID == "" || r.EndID == "" || r.ApplyDay < 0 || r.EffectiveDay < 0 {
		return false
	}
	switch r.Kind {
	case KindAmountChange:
		return r.NewSumAssured > 0
	case KindPayPeriodChange:
		return r.NewPayPeriods > 0
	case KindBeneficiaryChange:
		return r.NewBeneficiary != ""
	default:
		return false
	}
}
