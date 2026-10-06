package deposit

import "sort"

// allocate 在申报期结束时按固定次序一次性确定每条扣项的押金受偿金额 Paid，
// 并生成第一笔（无争议部分）应退 tranche。
//
// 受偿规则：类别次序 欠租 > 损坏 > 清洁 > 其他；同类别按申报先后。
// 押金依次扣减，不足时排在后面的扣项按剩余额部分受偿，未受偿部分成为应收。
// 该结果此后不可变：撤销只能发生在申报期内，争议不影响其他扣项，
// 裁定只改本扣项且不得追加押金受偿。因此争议/裁定的重算只需读取该扣项
// 自身的 Paid（O(1)），与扣项总数无关——这是可验证的复杂度保证。
func (s *Service) allocate(l *lease, now int) {
	if l.allocated {
		return
	}
	sort.SliceStable(l.order, func(i, j int) bool {
		if l.order[i].Category != l.order[j].Category {
			return l.order[i].Category < l.order[j].Category
		}
		return l.order[i].Seq < l.order[j].Seq
	})
	remaining := l.deposit
	for _, c := range l.order {
		paid := min64(remaining, c.Amount)
		c.Paid = paid
		remaining -= paid
	}
	l.allocated = true
	// 第一笔应退 tranche：未受偿的全部押金，时限从申报期结束日起算。
	if remaining > 0 {
		l.tranches = append(l.tranches, &tranche{
			Amount: remaining,
			Start:  l.checkout + s.cfg.A,
		})
	}
}

// matureVested 惰性地把争议期已结束、仍未争议的扣项受偿金额归属房东。
// 争议期结束日 = 申报期结束日 + B。争议中扣项的冻结金额不受影响，
// 等待裁定；未争议扣项的 Paid 自此归属房东。
func (s *Service) matureVested(l *lease, now int) {
	if !l.allocated {
		return
	}
	end := l.checkout + s.cfg.A + s.cfg.B
	if now <= end {
		return
	}
	for _, c := range l.claims {
		if c.Status == csActive && c.Vested < c.Paid {
			c.Vested = c.Paid
		}
	}
}
