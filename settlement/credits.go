package settlement

import "container/heap"

// creditHeap 的顺序即规则要求的使用顺序：到期月从早到晚，同到期月按存入月从早到晚。
// 已用尽的笔在到达堆顶时弹出；已到期的笔在每月抵扣之后统一从堆顶弹出。
// 因此堆中永远只有「未用尽且未到期」的笔，规模与历史封账月数无关。
type creditHeap []*CreditLot

func (h creditHeap) Len() int { return len(h) }

func (h creditHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.ExpireMonth.equal(b.ExpireMonth) {
		return a.ExpireMonth.index() < b.ExpireMonth.index()
	}
	return a.DepositMonth.index() < b.DepositMonth.index()
}

func (h creditHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *creditHeap) Push(x any) { *h = append(*h, x.(*CreditLot)) }

func (h *creditHeap) Pop() any {
	old := *h
	n := len(old)
	lot := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return lot
}

func (h *creditHeap) clone() creditHeap {
	cp := make(creditHeap, len(*h))
	for i, lot := range *h {
		clone := *lot
		cp[i] = &clone
	}
	heap.Init(&cp)
	return cp
}

// activeBefore 返回 m 可使用的额度笔数：到期晚于 m，且存入早于 m。
// 仅用于诊断/测试；结算路径本身不做全堆扫描。
func (h *creditHeap) activeBefore(m Month) int {
	n := 0
	for _, lot := range *h {
		if lot.ExpireMonth.index() > m.index() && lot.DepositMonth.index() < m.index() {
			n++
		}
	}
	return n
}

// monthInput 是逐月结算纯函数的全部输入，不持有任何全局可变状态。
type monthInput struct {
	Month         Month
	Readings      []Reading
	Params        Params
	Prices        Prices
	IntervalCapWh int // 合同功率上限 × 间隔时长（Wh）
}

// settleMonth 执行一个月的全部结算步骤，就地变更 active（弹出用尽/到期笔、压入新笔）。
// 步骤顺序是规则的直接翻译：
//  1. 逐间隔汇总并做超限剔除；
//  2. 月度可计入上限分档；
//  3. 当月可计入先逐度抵扣当月下网；
//  4. 净下网用历史额度（存入早于本月、到期晚于本月）抵扣，剩余计费；
//  5. 净余上网存入一笔带到期额度；
//  6. 到期月 <= 本月的额度（不含刚存入时到期恰为以后月的情形）按本月余电单价付款清零。
func settleMonth(in monthInput, active *creditHeap) MonthResult {
	r := MonthResult{Month: in.Month}

	for _, rd := range in.Readings {
		r.ImportWh += rd.ImportWh
		r.ExportRawWh += rd.ExportWh
		over := rd.ExportWh - in.IntervalCapWh
		if over > 0 {
			r.CurtailedWh += over
		}
	}
	cappedRaw := r.ExportRawWh - r.CurtailedWh
	if cappedRaw > in.Params.MonthlyCreditableW {
		r.CreditableWh = in.Params.MonthlyCreditableW
		r.AboveCapWh = cappedRaw - in.Params.MonthlyCreditableW
	} else {
		r.CreditableWh = cappedRaw
	}

	netImport := r.ImportWh - r.CreditableWh
	if netImport <= 0 {
		r.SelfOffsetWh = r.ImportWh
		r.DepositedWh = -netImport
	} else {
		r.SelfOffsetWh = r.CreditableWh
		need := netImport
		for need > 0 {
			// 每轮重新取堆顶：上一轮只部分使用时堆顶未弹出，
			// 必须再次检查同一笔，而不是越过它。
			if active.Len() == 0 {
				break
			}
			top := (*active)[0]
			// 到期月严格早于本月的笔早已应付款：不会出现在正常封账路径中
			// （每月封账都会清），仅在试算跳转等场景兜底弹出，继续往后找。
			// 到期月恰等于本月的笔本月仍然可用，抵扣之后才到期。
			if top.ExpireMonth.index() < in.Month.index() {
				lot := heap.Pop(active).(*CreditLot)
				r.ExpiredPaidWh += lot.RemainingWh
				continue
			}
			if top.DepositMonth.index() >= in.Month.index() {
				break
			}
			take := top.RemainingWh
			if take > need {
				take = need
			}
			top.RemainingWh -= take
			need -= take
			r.HistoryUsedWh += take
			if top.RemainingWh == 0 {
				heap.Pop(active)
			}
		}
		r.BilledWh = need
	}

	if r.DepositedWh > 0 {
		lot := &CreditLot{
			DepositMonth: in.Month,
			ExpireMonth:  in.Month.add(in.Params.CreditValidMonths),
			RemainingWh:  r.DepositedWh,
		}
		r.NewCreditRemaining = r.DepositedWh
		heap.Push(active, lot)
	}

	for active.Len() > 0 && (*active)[0].ExpireMonth.index() <= in.Month.index() {
		lot := heap.Pop(active).(*CreditLot)
		r.ExpiredPaidWh += lot.RemainingWh
	}

	r.ImportBill = r.BilledWh * in.Prices.ImportPricePerWh
	r.SurplusPayment = (r.AboveCapWh + r.ExpiredPaidWh) * in.Prices.SurplusPricePerWh
	return r
}
