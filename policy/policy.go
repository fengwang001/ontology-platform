package policy

// Policy 单张保单的状态机。所有金额单位为分，时刻为整数天。
// 不变量：settle(day) 之后，所有 <= day 的应缴、宽限期满、垫交、
// 中止、终止事件均已依次结算完毕。
type Policy struct {
	cfg        Config
	day        int   // 当前时刻
	state      State // 当前状态
	paidCount  int   // 已缴期数（含首期与垫交期）
	graceDue   int   // 宽限中：该期应缴日
	lapseDay   int   // 中止起算日（宽限期满次日）
	waitingEnd int   // 等待期最后一日（含）；小于当前日即不在等待期
	ledger     loanLedger
	settleOps  int64 // 已结算事件计数，用于性能验证
}

func newPolicy(cfg Config) *Policy {
	return &Policy{
		cfg:        cfg,
		day:        cfg.EffectiveDay,
		state:      StateActive,
		paidCount:  1, // 首期在登记时视为已缴
		waitingEnd: cfg.EffectiveDay + cfg.WaitingDays - 1,
		ledger:     loanLedger{asOfDay: cfg.EffectiveDay, ratePPM: cfg.DailyRatePPM},
	}
}

// nextDue 下一期应缴日：第 n 期应缴日 = 生效日 + (n-1) 个周期。
func (p *Policy) nextDue() int {
	return p.cfg.EffectiveDay + p.paidCount*p.cfg.PeriodDays
}

// cashValue 当前已缴期数对应的现金价值；超出表尾取最后一项（表非递减）。
func (p *Policy) cashValue() int64 {
	cv := p.cfg.CashValue
	if p.paidCount >= len(cv) {
		return cv[len(cv)-1]
	}
	return cv[p.paidCount]
}

// settle 把 <= day 的所有事件按时间先后依次结算，每个事件恰好一次，
// 事件数只与本次推进区间内发生的事件有关，与历史长短无关。
func (p *Policy) settle(day int) {
	for {
		switch p.state {
		case StateActive:
			if p.nextDue() > day {
				p.day = day
				return
			}
			// 应缴日当天未缴即进入宽限中。
			p.settleOps++
			p.state = StateGrace
			p.graceDue = p.nextDue()
		case StateGrace:
			expiry := p.graceDue + p.cfg.GraceDays // 宽限期满次日
			if expiry > day {
				p.day = day
				return
			}
			p.settleOps++
			p.ledger.accrueTo(expiry)
			if p.cashValue()-p.ledger.balanceAt(expiry) >= p.cfg.Premium {
				// 净值不小于一期保费：自动垫交，全有或全无。
				p.ledger.borrow(p.cfg.Premium, expiry)
				p.paidCount++
				p.state = StateActive
			} else {
				p.state = StateLapsed
				p.lapseDay = expiry
			}
		case StateLapsed:
			term := p.lapseDay + p.cfg.RevivalDays // 复效期满次日
			if term > day {
				p.day = day
				return
			}
			p.settleOps++
			p.state = StateTerminated
			p.ledger.freeze(term)
		case StateTerminated:
			p.day = day
			return
		}
	}
}

// inWaiting 当前是否处于等待期（登记或复效后 WaitingDays 天内）。
func (p *Policy) inWaiting() bool {
	return p.day <= p.waitingEnd
}

// owed 当前欠费金额：到期未缴保费 + 借款本息；终止后无欠费。
func (p *Policy) owed() int64 {
	switch p.state {
	case StateGrace, StateLapsed:
		return p.cfg.Premium + p.ledger.balanceAt(p.day)
	case StateActive:
		return p.ledger.balanceAt(p.day)
	default:
		return 0
	}
}

func (p *Policy) snapshot() Snapshot {
	s := Snapshot{
		Day:           p.day,
		State:         p.state,
		PaidCount:     p.paidCount,
		NextDueDay:    p.nextDue(),
		GraceDueDay:   -1,
		LapseDay:      -1,
		LoanPrincipal: p.ledger.principal,
		LoanInterest:  p.ledger.interestAt(p.day),
		OwedAmount:    p.owed(),
		InWaiting:     p.inWaiting(),
		SettleOps:     p.settleOps,
	}
	if p.state == StateGrace {
		s.GraceDueDay = p.graceDue
	}
	if p.state == StateLapsed || p.state == StateTerminated {
		s.LapseDay = p.lapseDay
	}
	return s
}
