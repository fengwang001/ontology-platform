package policy

// Policy 单张保单的状态机。方法均非并发安全，由 Engine 串行化调用。
type Policy struct {
	cfg          Config
	now          int64 // 当前时刻（整数天，只前进）
	state        State
	sched        paymentSchedule
	loan         loanBook
	lapseDay     int64 // 中止起算日（宽限期满次日）
	lapseOwed    int64 // 中止时到期未缴期数（中止期间冻结，不计新周期）
	waitingUntil int64 // 等待期截止日（不含），登记与每次复效时重算
	settleSteps  int64 // 状态推进迭代计数：测试钩子，用于证明推进开销为 O(1)
}

func newPolicy(cfg Config) *Policy {
	return &Policy{
		cfg:   cfg,
		now:   cfg.EffectiveDay,
		state: StateActive,
		sched: paymentSchedule{
			effectiveDay: cfg.EffectiveDay,
			periodDays:   cfg.PeriodDays,
			paid:         1, // 首期在登记时视为已缴
		},
		loan:         loanBook{rate: cfg.LoanRatePerMyriad, lastDay: cfg.EffectiveDay},
		waitingUntil: cfg.EffectiveDay + cfg.WaitingDays,
	}
}

// cashValue 当前已缴期数对应的现金价值（超出表长取末项）。
func (p *Policy) cashValue() int64 {
	idx := p.sched.paid
	if idx >= int64(len(p.cfg.CashValues)) {
		idx = int64(len(p.cfg.CashValues)) - 1
	}
	return p.cfg.CashValues[idx]
}

// settle 把状态推进到 day（要求 day >= now），依次结算 (now, day] 之间所有
// 应缴、宽限期满、垫交、中止、终止事件。迭代次数只与该窗口内的事件数成正比，
// 与已经历的缴费期数、历史借款笔数无关。
func (p *Policy) settle(day int64) {
	for {
		p.settleSteps++
		switch p.state {
		case StateActive, StateGrace:
			nextDue := p.sched.nextDue()
			if day < nextDue {
				p.finish(day)
				return
			}
			// 应缴日当天未缴即进入宽限中（当天仍可缴）。
			p.state = StateGrace
			graceEnd := nextDue + p.cfg.GraceDays // 宽限期满次日
			if day < graceEnd {
				p.finish(day)
				return
			}
			// 宽限期满：先把借款利息结到结算日，再判定垫交（全有或全无）。
			p.loan.accrueTo(graceEnd)
			if p.cashValue()-p.loan.total() >= p.cfg.PremiumCents {
				p.loan.borrow(p.cfg.PremiumCents)
				p.sched.paid++
				p.state = StateActive
				continue
			}
			p.state = StateLapsed
			p.lapseDay = graceEnd
			p.lapseOwed = p.sched.duePeriods(graceEnd)
		case StateLapsed:
			// 复效期为自中止起算日起 ReinstateDays 天，期满次日终止。
			if day >= p.lapseDay+p.cfg.ReinstateDays {
				p.state = StateTerminated
			}
			p.finish(day)
			return
		case StateTerminated:
			p.finish(day)
			return
		}
	}
}

// finish 结束一次推进：结息到 day 并更新当前时刻。
func (p *Policy) finish(day int64) {
	p.loan.accrueTo(day)
	p.now = day
}

// owedCents 当前欠费金额（到期未缴保费；终止后不再欠费）。
func (p *Policy) owedCents() int64 {
	switch p.state {
	case StateActive, StateGrace:
		return p.sched.duePeriods(p.now) * p.cfg.PremiumCents
	case StateLapsed:
		return p.lapseOwed * p.cfg.PremiumCents
	default:
		return 0
	}
}

// payPremium 主动缴费：金额须恰好为整数期保费；仅有效或宽限中可缴。
func (p *Policy) payPremium(amount int64) error {
	if amount <= 0 || amount%p.cfg.PremiumCents != 0 {
		return ErrInvalidParam
	}
	if p.state != StateActive && p.state != StateGrace {
		return ErrStateNotAllowed
	}
	p.sched.paid += amount / p.cfg.PremiumCents
	if p.state == StateGrace && p.sched.nextDue() > p.now {
		p.state = StateActive
	}
	return nil
}

// repayLoan 主动还款：仅有效状态可还；先还利息后还本金。
func (p *Policy) repayLoan(amount int64) error {
	if amount <= 0 {
		return ErrInvalidParam
	}
	if p.state != StateActive {
		return ErrStateNotAllowed
	}
	if amount > p.loan.total() {
		return ErrOverpayment
	}
	p.loan.repay(amount)
	return nil
}

// reinstate 复效：仅中止状态可申请；须一次性补缴中止期间全部到期保费
// 与已有借款本息，不足报「补缴不足」且不留任何部分款项。
func (p *Policy) reinstate(amount int64) error {
	if amount < 0 {
		return ErrInvalidParam
	}
	if p.state != StateLapsed {
		return ErrStateNotAllowed
	}
	required := p.lapseOwed*p.cfg.PremiumCents + p.loan.total()
	if amount < required {
		return ErrInsufficientPayment
	}
	p.loan.clear()
	p.sched.paid += p.lapseOwed
	// 中止期间不计新周期：复效后缴费计划自复效生效日起重排，
	// 下一应缴日 = 复效日 + 一个缴费周期。
	p.sched.effectiveDay = p.now + p.cfg.PeriodDays - p.sched.paid*p.cfg.PeriodDays
	p.state = StateActive
	p.waitingUntil = p.now + p.cfg.WaitingDays
	return nil
}
