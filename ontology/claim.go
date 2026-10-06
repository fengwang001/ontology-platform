package ontology

import "sort"

// settle 结算一笔理赔，全有或全无。调用方须持有 p.mu。
// 返回的赔付额与输入明细一一对应（按输入顺序）。
func (p *Policy) settle(in ClaimInput) ([]int64, *Error) {
	m, ok := p.members[in.MemberID]
	if !ok {
		return nil, errOf(ErrMemberNotFound, p.id, in.MemberID)
	}
	for _, ln := range in.Lines {
		if _, ok := p.items[ln.ItemID]; !ok {
			return nil, errOf(ErrItemNotFound, p.id, ln.ItemID)
		}
	}
	if _, dup := p.claims[in.ID]; dup {
		return nil, errOf(ErrClaimExists, p.id, in.ID)
	}
	if m.capped() {
		return nil, errOf(ErrCapped, p.id, in.MemberID)
	}
	for _, ln := range in.Lines {
		if ln.Day < p.startDay {
			return nil, errOf(ErrDayNotCovered, p.id, in.ID)
		}
	}

	// 明细按项目编号升序、同项目按发生日升序依次扣减。
	order := make([]int, len(in.Lines))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		la, lb := in.Lines[order[a]], in.Lines[order[b]]
		if la.ItemID != lb.ItemID {
			return la.ItemID < lb.ItemID
		}
		return la.Day < lb.Day
	})

	payouts := make([]int64, len(in.Lines))
	settled := make([]settledLine, 0, len(in.Lines))
	for _, idx := range order {
		ln := in.Lines[idx]
		year := p.yearOf(ln.Day)
		it := p.items[ln.ItemID]
		pay := min64(ln.Amount,
			remaining(it.endorse.get(year, it.annualBase), it.used[memberYear{in.MemberID, year}]),
			remaining(m.endorse.get(year, m.annualBase), m.annualUsed[year]),
			remaining(p.familyEndors.get(year, p.familyBase), p.familyUsed[year]),
			remaining(m.lifetimeLimit, m.lifetimeUsed),
		)
		it.used[memberYear{in.MemberID, year}] += pay
		m.annualUsed[year] += pay
		p.familyUsed[year] += pay
		m.lifetimeUsed += pay
		p.days.add(ln.Day)
		p.ops++
		payouts[idx] = pay
		settled = append(settled, settledLine{itemID: ln.ItemID, day: ln.Day, year: year, paid: pay})
	}
	p.claims[in.ID] = &claim{id: in.ID, memberID: in.MemberID, lines: settled}
	p.memberClaims[in.MemberID] = append(p.memberClaims[in.MemberID], in.ID)
	return payouts, nil
}

// reverse 冲正一笔已结算理赔，逐明细退回原年度与项目。
// 已用额记账天然保证恢复后剩余额不超过批改后的限额。调用方须持有 p.mu。
func (p *Policy) reverse(claimID string) *Error {
	c, ok := p.claims[claimID]
	if !ok {
		return errOf(ErrClaimNotFound, p.id, claimID)
	}
	stack := p.memberClaims[c.memberID]
	if len(stack) == 0 || stack[len(stack)-1] != claimID {
		return errOf(ErrNotLastClaim, p.id, claimID)
	}
	m := p.members[c.memberID]
	for _, ln := range c.lines {
		it := p.items[ln.itemID]
		it.used[memberYear{c.memberID, ln.year}] -= ln.paid
		m.annualUsed[ln.year] -= ln.paid
		p.familyUsed[ln.year] -= ln.paid
		m.lifetimeUsed -= ln.paid
		p.days.remove(ln.day)
		p.ops++
	}
	delete(p.claims, claimID) // 理赔号释放
	p.memberClaims[c.memberID] = stack[:len(stack)-1]
	return nil
}

func min64(v int64, rest ...int64) int64 {
	for _, x := range rest {
		if x < v {
			v = x
		}
	}
	return v
}
