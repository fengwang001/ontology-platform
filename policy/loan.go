package policy

// loanBook 垫交借款账（聚合口径）。
//
// 所有垫交借款按聚合本金逐日计息：单日利息 = ⌈本金 × 日利率 / 10000⌉（分，
// 向上取整），单利、不计复利。聚合口径使计息与还款的开销不随历史借款笔数
// 增长（O(1)），这是相对逐笔台账的关键取舍。
type loanBook struct {
	principal int64 // 未还本金（分）
	interest  int64 // 已累计未还利息（分）
	lastDay   int64 // 利息已累计至该日
	rate      int64 // 日利率（万分之一）
	count     int64 // 累计垫交笔数（仅统计用）
}

// ceilDiv 非负整数的向上取整除法。
func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// perDiem 当前本金对应的单日利息（分，向上取整）。
func (l *loanBook) perDiem() int64 { return ceilDiv(l.principal*l.rate, 10000) }

// accrueTo 把利息逐日累计到 day（要求 day >= lastDay）。
func (l *loanBook) accrueTo(day int64) {
	if day <= l.lastDay {
		return
	}
	l.interest += l.perDiem() * (day - l.lastDay)
	l.lastDay = day
}

// borrow 形成一笔垫交借款（调用方已把利息结到借款日）。
func (l *loanBook) borrow(amount int64) {
	l.principal += amount
	l.count++
}

// total 借款本息合计。
func (l *loanBook) total() int64 { return l.principal + l.interest }

// repay 还款，先还利息后还本金；调用方保证 amount <= total()。
func (l *loanBook) repay(amount int64) {
	if amount <= l.interest {
		l.interest -= amount
		return
	}
	l.principal -= amount - l.interest
	l.interest = 0
}

// clear 结清全部借款本息（复效时调用）。
func (l *loanBook) clear() {
	l.principal = 0
	l.interest = 0
}
