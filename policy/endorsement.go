package policy

import "container/heap"

// EndorsementInput 批改预约参数。
type EndorsementInput struct {
	ID            string
	Type          EndorsementType
	ApplyDay      int64 // 申请日
	EffDay        int64 // 生效日
	NewSumInsured int64 // 保额变更：新基本保额（分）
	NewTerm       int   // 缴费期变更：新缴费期（年）
	Beneficiary   string
}

// ScheduleEndorsement 以预约状态登记批改。
// 生效日早于申请日、早于当前时刻或早于已生效批改中最晚生效日的，报追溯批改。
func (e *Engine) ScheduleEndorsement(policyID string, in EndorsementInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if in.ID == "" || in.ApplyDay < 0 || in.EffDay < 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	switch in.Type {
	case EndorsementSumInsured:
		if in.NewSumInsured <= 0 {
			return newErr(ErrInvalidParam, "参数非法")
		}
	case EndorsementPaymentTerm:
		if in.NewTerm <= 0 {
			return newErr(ErrInvalidParam, "参数非法")
		}
	case EndorsementBeneficiary:
	default:
		return newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[policyID]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	if _, dup := p.endorsements[in.ID]; dup {
		return newErr(ErrEndorsementDuplicate, "批改重复")
	}
	if in.EffDay < in.ApplyDay || in.EffDay < p.now || in.EffDay < p.maxEffDay {
		return newErr(ErrRetroactive, "追溯批改")
	}
	en := &endorsement{
		id:            in.ID,
		typ:           in.Type,
		applyDay:      in.ApplyDay,
		effDay:        in.EffDay,
		seq:           p.seqCounter,
		state:         StateScheduled,
		newSumInsured: in.NewSumInsured,
		newTerm:       in.NewTerm,
		beneficiary:   in.Beneficiary,
	}
	p.seqCounter++
	p.endorsements[in.ID] = en
	heap.Push(&p.queue, en)
	return nil
}

// CancelEndorsement 撤销预约状态的批改；已生效报“已生效”，待补缴报“待补缴”。
func (e *Engine) CancelEndorsement(policyID, endorsementID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.policies[policyID]
	if !ok {
		return newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return newErr(ErrTerminated, "已终态")
	}
	en, ok := p.endorsements[endorsementID]
	if !ok {
		return newErr(ErrEndorsementNotFound, "批改不存在")
	}
	switch en.state {
	case StateEffective:
		return newErr(ErrAlreadyEffective, "已生效")
	case StatePendingTopUp:
		return newErr(ErrPendingTopUp, "待补缴")
	}
	heap.Remove(&p.queue, en.heapIndex)
	delete(p.endorsements, endorsementID)
	return nil
}

// PayTopUp 待补缴批改的补缴到账，返回补缴金额并令批改生效。
func (e *Engine) PayTopUp(policyID, endorsementID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.policies[policyID]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	en, ok := p.endorsements[endorsementID]
	if !ok {
		return 0, newErr(ErrEndorsementNotFound, "批改不存在")
	}
	switch en.state {
	case StateEffective:
		return 0, newErr(ErrAlreadyEffective, "已生效")
	case StateScheduled:
		return 0, newErr(ErrInvalidParam, "参数非法: 补缴尚未产生")
	}
	amount := en.topUp
	p.totalPaid += amount
	p.sumInsured = en.newSumInsured
	p.premium = en.newPremium
	p.pendingTopUpCount--
	p.markEffective(en)
	return amount, nil
}

// effectuate 在时刻推进中生效一条到期预约批改。
// 保额变更若本年度已缴且保费增加，则转入待补缴，不作任何账务变更。
func (p *Policy) effectuate(en *endorsement) Event {
	switch en.typ {
	case EndorsementSumInsured:
		newPremium := ceilDiv(p.premium*en.newSumInsured, p.sumInsured)
		delta := newPremium - p.premium
		year := p.yearOf(en.effDay)
		remaining := p.effDate + (year+1)*daysPerYear - en.effDay
		if p.paidYears[year] && delta > 0 {
			en.newPremium = newPremium
			en.topUp = ceilDiv(delta*remaining, daysPerYear)
			en.state = StatePendingTopUp
			p.pendingTopUpCount++
			return Event{EndorsementID: en.id, Kind: EventPendingTopUp, TopUp: en.topUp}
		}
		var refund int64
		if p.paidYears[year] && delta < 0 {
			refund = (-delta) * remaining / daysPerYear
			p.totalPaid -= refund
		}
		p.sumInsured = en.newSumInsured
		p.premium = newPremium
		p.markEffective(en)
		return Event{EndorsementID: en.id, Kind: EventEffective, Refund: refund}
	case EndorsementPaymentTerm:
		p.paymentTerm = en.newTerm
		p.markEffective(en)
		return Event{EndorsementID: en.id, Kind: EventEffective}
	case EndorsementBeneficiary:
		p.beneficiary = en.beneficiary
		p.markEffective(en)
		return Event{EndorsementID: en.id, Kind: EventEffective}
	}
	return Event{}
}

// markEffective 置批改已生效并维护已生效最晚生效日。
func (p *Policy) markEffective(en *endorsement) {
	en.state = StateEffective
	if en.effDay > p.maxEffDay {
		p.maxEffDay = en.effDay
	}
}
