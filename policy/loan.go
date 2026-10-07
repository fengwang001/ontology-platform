package policy

const perMyriad = 10000

// ceilDiv 向上取整除法，要求 a >= 0、b > 0。
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

// loanLedger 借款账：全部借款按同一日利率聚合记账，不逐笔存储，
// 因此查询与计息开销与历史借款笔数无关（O(1)）。
// 利息按日单利逐日累计，每日利息 = ceil(本金 * 日利率 / 10000)，按分向上取整。
type loanLedger struct {
	principal int64 // 未还本金（分）
	interest  int64 // 截至 asOfDay 已冻结累计的利息（分）
	asOfDay   int   // 利息已结算到的那一天
	ratePPM   int64 // 日利率（万分之一）
}

// dailyInterest 当前本金对应的每日利息（按分向上取整）。
func (l *loanLedger) dailyInterest() int64 {
	return ceilDiv(l.principal*l.ratePPM, perMyriad)
}

// interestAt 任意时刻的累计利息，O(1)。
func (l *loanLedger) interestAt(day int) int64 {
	return l.interest + l.dailyInterest()*int64(day-l.asOfDay)
}

// balanceAt 任意时刻的本息合计，O(1)。
func (l *loanLedger) balanceAt(day int) int64 {
	return l.principal + l.interestAt(day)
}

// accrueTo 把利息冻结结算到 day。
func (l *loanLedger) accrueTo(day int) {
	l.interest = l.interestAt(day)
	l.asOfDay = day
}

// borrow 形成一笔借款（自动垫交），先结息再增本金。
func (l *loanLedger) borrow(amount int64, day int) {
	l.accrueTo(day)
	l.principal += amount
}

// repay 还款，先还利息后还本金；超过本息合计报 ErrOverpayment 且账不变。
func (l *loanLedger) repay(amount int64, day int) error {
	if amount > l.balanceAt(day) {
		return ErrOverpayment
	}
	l.accrueTo(day)
	if amount <= l.interest {
		l.interest -= amount
		return nil
	}
	amount -= l.interest
	l.interest = 0
	l.principal -= amount
	return nil
}

// clear 结清全部借款（复效时），先结息再清零。
func (l *loanLedger) clear(day int) {
	l.accrueTo(day)
	l.principal = 0
	l.interest = 0
}

// freeze 终止后停止计息。
func (l *loanLedger) freeze(day int) {
	l.accrueTo(day)
	l.ratePPM = 0
}
