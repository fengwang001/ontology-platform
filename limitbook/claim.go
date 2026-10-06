package limitbook

import "sort"

// Detail 一条费用明细。金额为正整数分，发生日为非负整数天。
type Detail struct {
	ItemID string
	Day    int64
	Amount int64
}

// Claim 一笔理赔。
type Claim struct {
	ID       string
	MemberID string
	Details  []Detail
}

// Settlement 理赔结算结果。Paid 与 Claim.Details 的输入顺序一一对应。
type Settlement struct {
	ClaimID string
	Total   int64
	Paid    []int64
}

// claimRecord 是已受理理赔的留痕，冲正时按记录的年度与项目精确退回。
type claimRecord struct {
	memberID string
	details  []Detail
	paid     []int64
	years    []int64
}

// validateClaim 校验不依赖保单上下文的理赔参数（拒绝次序：参数非法最优先）。
func validateClaim(c Claim) error {
	if c.ID == "" || c.MemberID == "" || len(c.Details) == 0 {
		return newErr(ErrInvalidParam, "理赔号、成员编号不能为空且至少含一条明细")
	}
	for _, d := range c.Details {
		if d.ItemID == "" || d.Day < 0 || d.Amount <= 0 {
			return newErr(ErrInvalidParam, "明细项目编号不能为空、发生日非负、金额为正整数分")
		}
	}
	return nil
}

// settle 在保单锁内完成全部校验与扣减，任一明细被拒则整笔拒绝、全有或全无。
// 校验次序固定：成员不存在 > 项目不存在 > 理赔已存在 > 已封顶 > 发生日未承保。
func (p *Policy) settle(c Claim) (*Settlement, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	m, ok := p.members[c.MemberID]
	if !ok {
		return nil, newErr(ErrMemberNotFound, "成员 %s", c.MemberID)
	}
	for _, d := range c.Details {
		if _, ok := p.items[d.ItemID]; !ok {
			return nil, newErr(ErrItemNotFound, "项目 %s", d.ItemID)
		}
	}
	if _, ok := p.claims[c.ID]; ok {
		return nil, newErr(ErrClaimExists, "理赔 %s", c.ID)
	}
	if p.lifetimeRemaining(m) == 0 {
		return nil, newErr(ErrCapped, "成员 %s 终身限额已耗尽", c.MemberID)
	}
	for _, d := range c.Details {
		if d.Day < p.start {
			return nil, newErr(ErrDayNotCovered, "发生日 %d 早于承保起始日 %d", d.Day, p.start)
		}
	}

	// 扣减次序：项目编号升序，同项目按发生日升序；稳定排序保证同键
	// 明细按输入顺序处理，结果可精确复现。
	order := make([]int, len(c.Details))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		da, db := c.Details[order[a]], c.Details[order[b]]
		if da.ItemID != db.ItemID {
			return da.ItemID < db.ItemID
		}
		return da.Day < db.Day
	})

	paid := make([]int64, len(c.Details))
	years := make([]int64, len(c.Details))
	var total int64
	for _, i := range order {
		d := c.Details[i]
		y := p.yearOf(d.Day)
		it := p.items[d.ItemID]
		pay := min(d.Amount,
			p.itemRemaining(m, it, y),
			p.memberRemaining(m, y),
			p.familyRemaining(y),
			p.lifetimeRemaining(m))
		if pay > 0 {
			p.addItemUsed(m, d.ItemID, y, pay)
			p.ops += 3
			m.yearUsed[y] += pay
			p.familyUsed[y] += pay
			m.lifetimeUsed += pay
		}
		paid[i] = pay
		years[i] = y
		total += pay
		p.ops++
		p.dayCounts[d.Day]++
		if d.Day > p.maxDay {
			p.maxDay = d.Day
		}
	}

	p.claims[c.ID] = &claimRecord{
		memberID: c.MemberID,
		details:  append([]Detail(nil), c.Details...),
		paid:     paid,
		years:    years,
	}
	m.claims = append(m.claims, c.ID)
	return &Settlement{ClaimID: c.ID, Total: total, Paid: paid}, nil
}

// reverse 冲正一笔已结算理赔：仅允许该成员受理次序最后的一笔。
// 恢复逐明细精确退回到原来扣减的年度与项目；剩余额由 max(0, 限额-已用)
// 派生，天然不会超过批改后的限额，超过部分不恢复。
func (p *Policy) reverse(claimID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, ok := p.claims[claimID]
	if !ok {
		return newErr(ErrClaimNotFound, "理赔 %s", claimID)
	}
	m := p.members[rec.memberID]
	if top := m.claims[len(m.claims)-1]; top != claimID {
		return newErr(ErrNotLast, "理赔 %s 非成员 %s 的末笔", claimID, rec.memberID)
	}
	m.claims = m.claims[:len(m.claims)-1]
	delete(p.claims, claimID)
	for i, d := range rec.details {
		if pay := rec.paid[i]; pay > 0 {
			y := rec.years[i]
			m.itemUsed[d.ItemID][y] -= pay
			m.yearUsed[y] -= pay
			p.familyUsed[y] -= pay
			m.lifetimeUsed -= pay
		}
		p.dayCounts[d.Day]--
		if p.dayCounts[d.Day] == 0 {
			delete(p.dayCounts, d.Day)
			if d.Day == p.maxDay {
				p.recomputeMaxDay()
			}
		}
	}
	return nil
}

// recomputeMaxDay 仅在冲正删除了最晚发生日时触发，不在结算路径上。
func (p *Policy) recomputeMaxDay() {
	latest := int64(-1)
	for d := range p.dayCounts {
		if d > latest {
			latest = d
		}
	}
	p.maxDay = latest
}
