package policy

// Surrender 整单退保。犹豫期内退还全部实缴减工本费（不足为零），
// 犹豫期后退还退保日现金价值。存在预约或待补缴批改时报“存在未生效批改”。
// 退保后保单终止，为终态。
func (e *Engine) Surrender(id string, loan int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if p.hasPending() {
		return 0, newErr(ErrHasPendingEndorsement, "存在未生效批改")
	}
	var refund int64
	if p.inHesitation(p.now) {
		refund = p.totalPaid - p.issueFee
		if refund < 0 {
			refund = 0
		}
	} else {
		refund = p.cashValueAt(p.now, loan)
	}
	p.terminated = true
	return refund, nil
}

// PartialSurrender 部分退保，仅犹豫期后允许。按减少保额占比退还现金价值
// 的同比例部分（向下取整），并同比例调减后续年缴保费（向上取整）。
// 退保后保额不得低于登记最低保额。
func (e *Engine) PartialSurrender(id string, reduce, loan int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if reduce <= 0 || loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if reduce >= p.sumInsured {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	if p.inHesitation(p.now) {
		return 0, newErr(ErrInHesitation, "犹豫期内不允许部分退保")
	}
	if p.hasPending() {
		return 0, newErr(ErrHasPendingEndorsement, "存在未生效批改")
	}
	newSumInsured := p.sumInsured - reduce
	if newSumInsured < p.minSumInsured {
		return 0, newErr(ErrBelowMinSumInsured, "低于最低保额")
	}
	refund := p.cashValueAt(p.now, loan) * reduce / p.sumInsured
	p.premium = ceilDiv(p.premium*newSumInsured, p.sumInsured)
	p.sumInsured = newSumInsured
	return refund, nil
}
