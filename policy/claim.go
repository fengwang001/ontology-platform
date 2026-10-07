package policy

// adjudicate 出险判定：以出险日（已推进结算）所处状态为准。
// 等待期检查优先于赔付计算；宽限中赔付全额减去全部到期未缴保费。
func (p *Policy) adjudicate() ClaimResult {
	switch p.state {
	case StateLapsed:
		return ClaimResult{Reason: ReasonLapsed}
	case StateTerminated:
		return ClaimResult{Reason: ReasonTerminated}
	}
	if p.now < p.waitingUntil {
		return ClaimResult{Reason: ReasonWaiting}
	}
	if p.state == StateGrace {
		amount := p.cfg.SumAssuredCents - p.owedCents()
		if amount < 0 {
			amount = 0
		}
		return ClaimResult{Pay: amount > 0, AmountCents: amount, Reason: ReasonGraceDeducted}
	}
	return ClaimResult{Pay: true, AmountCents: p.cfg.SumAssuredCents, Reason: ReasonFull}
}

// snapshot 当前时刻的保单快照。
func (p *Policy) snapshot() Snapshot {
	return Snapshot{
		Day:           p.now,
		State:         p.state,
		PaidPeriods:   p.sched.paid,
		NextDueDay:    p.sched.nextDue(),
		OwedCents:     p.owedCents(),
		LoanPrincipal: p.loan.principal,
		LoanInterest:  p.loan.interest,
		LoanCount:     p.loan.count,
		WaitingUntil:  p.waitingUntil,
	}
}
