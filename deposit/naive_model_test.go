package deposit

// 朴素模型：与生产代码完全独立编写的线性重放参考实现。
// 每一步之后都从原始事件清单重新推导全部账目（不依赖任何缓存），
// 因此它天然地复算所有扣项，刻意保持“笨而显然正确”。

type nClaim struct {
	id          int
	cat         Category
	amount      int64
	withdrawnAt int
	disputedAt  int
	adjudgedAt  int
	adjudged    int64
}

type nRefund struct {
	principal int64
	liability int64
	at        int
}

type naiveState struct {
	cfg     Config
	coDay   int
	deposit int64
	claims  []*nClaim
	nextID  int
	refunds []nRefund
	lastNow int
	ready   bool // 已退房
}

func newNaive(cfg Config) *naiveState { return &naiveState{cfg: cfg} }

// 申报期结束日
func (n *naiveState) fileEnd() int { return n.coDay + n.cfg.A }

// 争议期结束日
func (n *naiveState) disputeEnd() int { return n.fileEnd() + n.cfg.B }

// paid 为每条（未撤销）扣项在申报期截止时的押金受偿额，按固定次序推导。
func (n *naiveState) paid() map[int]int64 {
	live := make([]*nClaim, 0, len(n.claims))
	for _, c := range n.claims {
		if c.withdrawnAt < 0 {
			live = append(live, c)
		}
	}
	// 独立的朴素排序：类别优先，同类别按 id（=申报先后）。
	for i := 1; i < len(live); i++ {
		for j := i; j > 0; j-- {
			a, b := live[j-1], live[j]
			less := a.cat < b.cat || (a.cat == b.cat && a.id < b.id)
			if !less {
				live[j-1], live[j] = b, a
			}
		}
	}
	paid := map[int]int64{}
	rem := n.deposit
	for _, c := range live {
		p := c.amount
		if p > rem {
			p = rem
		}
		paid[c.id] = p
		rem -= p
	}
	return paid
}

// tranches 推导应退租户的本金分笔：起始 tranche + 每笔裁定释放。
type nTranche struct {
	amount int64
	start  int
}

func (n *naiveState) tranches(paid map[int]int64) []nTranche {
	return n.tranchesAt(paid, true)
}

func (n *naiveState) tranchesAt(paid map[int]int64, filingClosed bool) []nTranche {
	var ts []nTranche
	used := int64(0)
	for _, p := range paid {
		used += p
	}
	if filingClosed {
		if rest := n.deposit - used; rest > 0 {
			ts = append(ts, nTranche{rest, n.fileEnd()})
		}
	}
	// 裁定按事件顺序处理；释放额 = 冻结额 - min(冻结额, 裁定额)。
	for _, c := range n.claims {
		if c.adjudgedAt >= 0 {
			frozen := paid[c.id]
			vest := frozen
			if c.adjudged < vest {
				vest = c.adjudged
			}
			if rel := frozen - vest; rel > 0 {
				ts = append(ts, nTranche{rel, c.adjudgedAt})
			}
		}
	}
	return ts
}

func (n *naiveState) liab(amount int64, start, now int) int64 {
	days := now - start - n.cfg.C - 1
	if days <= 0 {
		return 0
	}
	return amount * int64(days) * n.cfg.RateNum / n.cfg.RateDen
}

// 每步操作的结果：错误码（OK 表示成功）与退还本金/违约金。
type nResult struct {
	code      ErrCode
	id        int
	principal int64
	liability int64
}

func (n *naiveState) find(id int) *nClaim {
	for _, c := range n.claims {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (n *naiveState) doCheckout(now int, deposit int64) nResult {
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	if deposit <= 0 || deposit > maxAmount {
		return nResult{code: ErrAmount}
	}
	if n.ready {
		return nResult{code: ErrState}
	}
	n.ready = true
	n.coDay = now
	n.deposit = deposit
	n.lastNow = now
	return nResult{code: ErrOK}
}

func (n *naiveState) file(now int, cat Category, amount int64) nResult {
	if !n.ready {
		if now < n.lastNow {
			return nResult{code: ErrClockRollback}
		}
		return nResult{code: ErrNoLease}
	}
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	if !cat.valid() {
		return nResult{code: ErrIllegalArgument}
	}
	if now > n.fileEnd() {
		return nResult{code: ErrLate}
	}
	if amount <= 0 || amount > maxAmount {
		return nResult{code: ErrAmount}
	}
	c := &nClaim{id: n.nextID, cat: cat, amount: amount,
		withdrawnAt: -1, disputedAt: -1, adjudgedAt: -1}
	n.nextID++
	n.claims = append(n.claims, c)
	n.lastNow = now
	return nResult{code: ErrOK, id: c.id}
}

func (n *naiveState) withdraw(now, id int) nResult {
	if !n.ready {
		if now < n.lastNow {
			return nResult{code: ErrClockRollback}
		}
		return nResult{code: ErrNoLease}
	}
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	if now > n.fileEnd() {
		return nResult{code: ErrLate}
	}
	c := n.find(id)
	if c == nil || c.withdrawnAt >= 0 {
		return nResult{code: ErrState}
	}
	c.withdrawnAt = now
	n.lastNow = now
	return nResult{code: ErrOK}
}

func (n *naiveState) dispute(now, id int) nResult {
	if !n.ready {
		if now < n.lastNow {
			return nResult{code: ErrClockRollback}
		}
		return nResult{code: ErrNoLease}
	}
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	if now <= n.fileEnd() {
		return nResult{code: ErrState}
	}
	if now > n.disputeEnd() {
		return nResult{code: ErrLate}
	}
	c := n.find(id)
	if c == nil || c.withdrawnAt >= 0 || c.disputedAt >= 0 {
		return nResult{code: ErrState}
	}
	c.disputedAt = now
	n.lastNow = now
	return nResult{code: ErrOK}
}

func (n *naiveState) adjudge(now, id int, amount int64) nResult {
	if !n.ready {
		if now < n.lastNow {
			return nResult{code: ErrClockRollback}
		}
		return nResult{code: ErrNoLease}
	}
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	c := n.find(id)
	if c == nil || c.withdrawnAt >= 0 || c.disputedAt < 0 {
		return nResult{code: ErrState}
	}
	if c.adjudgedAt >= 0 {
		return nResult{code: ErrState}
	}
	if amount < 0 || amount > c.amount {
		return nResult{code: ErrAmount}
	}
	c.adjudgedAt = now
	c.adjudged = amount
	n.lastNow = now
	return nResult{code: ErrOK}
}

func (n *naiveState) refund(now int) nResult {
	if !n.ready {
		if now < n.lastNow {
			return nResult{code: ErrClockRollback}
		}
		return nResult{code: ErrNoLease}
	}
	if now < n.lastNow {
		return nResult{code: ErrClockRollback}
	}
	paid := n.paid()
	ts := n.tranchesAt(paid, now > n.fileEnd())
	var total int64
	for _, tr := range ts {
		total += tr.amount
	}
	var already int64
	for _, r := range n.refunds {
		already += r.principal
	}
	if total-already <= 0 {
		return nResult{code: ErrState}
	}
	principal := total - already
	var liability int64
	covered := already
	for _, tr := range ts {
		if covered >= tr.amount {
			covered -= tr.amount
			continue
		}
		liability += n.liab(tr.amount, tr.start, now)
	}
	n.refunds = append(n.refunds, nRefund{principal: principal, liability: liability, at: now})
	n.lastNow = now
	return nResult{code: ErrOK, principal: principal, liability: liability}
}

// accounts 在某时刻重算五项账目，供与生产快照逐项对照。
func (n *naiveState) accounts(now int) (refunded, vested, frozen, pending, receivable, refundable, liability int64) {
	closed := now > n.fileEnd()
	paid := map[int]int64{}
	if closed {
		paid = n.paid()
	}
	for _, c := range n.claims {
		if c.withdrawnAt >= 0 {
			continue
		}
		p := paid[c.id]
		switch {
		case c.adjudgedAt >= 0:
			v := p
			if c.adjudged < v {
				v = c.adjudged
			}
			vested += v
			receivable += c.adjudged - v
		case c.disputedAt >= 0:
			frozen += p
		default:
			if now > n.disputeEnd() {
				vested += p
			}
		}
		if c.adjudgedAt < 0 {
			receivable += c.amount - p
		}
	}
	ts := n.tranchesAt(paid, closed)
	covered := int64(0)
	for _, r := range n.refunds {
		covered += r.principal
	}
	for _, tr := range ts {
		if covered >= tr.amount {
			covered -= tr.amount
			continue
		}
		refundable += tr.amount
		liability += n.liab(tr.amount, tr.start, now)
	}
	for _, r := range n.refunds {
		refunded += r.principal
	}
	pending = n.deposit - refunded - vested - frozen
	return
}
