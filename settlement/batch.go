package settlement

// batch.go —— 日终批处理：判定、交割占用、罚金、强制了结。

// RunBatch 执行某营业日的日终批处理。
// 错误优先级：参数非法 > 非营业日 > 次序错误。
func (s *System) RunBatch(day int64, refPrices map[int64]int64) error {
	if day <= 0 || refPrices == nil {
		return ErrInvalidParam
	}
	for sec, p := range refPrices {
		if sec <= 0 || p <= 0 {
			return ErrInvalidParam
		}
	}
	idx, isBiz := s.bizDays[day]
	if !isBiz {
		return ErrNonBusinessDay
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var expected int64
	if s.lastBatchDay == 0 {
		expected = s.days[0]
	} else {
		prevIdx := s.bizDays[s.lastBatchDay]
		if prevIdx+1 >= len(s.days) {
			return ErrOrdering // 营业日集合之后没有更多营业日
		}
		expected = s.days[prevIdx+1]
	}
	if day != expected {
		return ErrOrdering // 跳过、重复或倒退
	}
	_ = idx

	candidates := s.collectCandidates(day)

	// 校验所有候选指令所需参考价。
	for _, ins := range candidates {
		if _, ok := refPrices[ins.Security]; !ok {
			return ErrInvalidParam
		}
	}

	// 批处理开始时的头寸快照；本批内收到的券/现金次日才可用。
	type secKey struct {
		acct string
		sec  int64
	}
	usedSec := make(map[secKey]int64)
	usedCash := make(map[string]int64)

	type leg struct {
		buyer, seller string
		sec           int64
		qty, cash     int64
	}
	legs := make([]leg, 0, len(candidates))

	report := BatchReport{Day: day, Decisions: make([]OrderDecision, 0, len(candidates))}
	nextOverdue := make([]*instruction, 0)

	for _, ins := range candidates {
		remaining := ins.Qty - ins.delivered
		sk := secKey{ins.Seller, ins.Security}
		sellerAvail := s.accounts[ins.Seller].securities[ins.Security] - usedSec[sk]
		buyerCash := s.accounts[ins.Buyer].cash - usedCash[ins.Buyer]
		buyerMax := buyerCash / ins.Price
		can := remaining
		if sellerAvail < can {
			can = sellerAvail
		}
		if buyerMax < can {
			can = buyerMax
		}
		if !ins.AllowPartial {
			if can < remaining {
				can = 0
			}
		}
		d := OrderDecision{
			ID:              ins.ID,
			RemainingBefore: remaining,
			SellerAvail:     sellerAvail,
			BuyerMaxQty:     buyerMax,
			Delivered:       can,
		}
		if can > 0 {
			legs = append(legs, leg{
				buyer: ins.Buyer, seller: ins.Seller,
				sec: ins.Security, qty: can, cash: can * ins.Price,
			})
			usedSec[sk] += can
			usedCash[ins.Buyer] += can * ins.Price
			ins.delivered += can
		}
		remaining -= can
		d.RemainingAfter = remaining
		if remaining == 0 {
			ins.status = StatusComplete
			report.Decisions = append(report.Decisions, d)
			continue
		}
		// 有失败：判定责任方（券不足优先于款不足）。
		d.Failed = true
		if sellerAvail < remaining {
			d.Responsible = "seller"
		} else {
			d.Responsible = "buyer"
		}
		ins.failDays++

		// 罚金：按日独立，ceil(cash * bps / 10000)。
		cashAmount := remaining * ins.Price
		penalty := ceilDiv(cashAmount*s.penBPS, 10000)
		d.Penalty = penalty
		responsible, counterparty := ins.Seller, ins.Buyer
		if d.Responsible == "buyer" {
			responsible, counterparty = ins.Buyer, ins.Seller
		}
		s.accounts[responsible].feePay += penalty
		s.accounts[counterparty].feeRecv += penalty

		if ins.failDays >= s.maxFail {
			// 强制了结：剩余量作废，罚金已计入。
			d.ForceClosed = true
			ins.status = StatusForceClosed
			if d.Responsible == "seller" {
				ref := refPrices[ins.Security]
				comp := ref*remaining - cashAmount
				if comp < 0 {
					comp = 0
				}
				d.Compensation = comp
				s.accounts[ins.Seller].compPay += comp
				s.accounts[ins.Buyer].compRecv += comp
			}
		} else {
			if ins.delivered > 0 {
				ins.status = StatusPartial
			} else {
				ins.status = StatusPending
			}
			nextOverdue = append(nextOverdue, ins)
		}
		report.Decisions = append(report.Decisions, d)
	}

	// 券款对付：在一致快照下提交全部成交腿，任一头寸不足则整体拒绝。
	type dSec struct {
		acct string
		sec  int64
	}
	dSecQty := make(map[dSec]int64)
	dCash := make(map[string]int64)
	for _, lg := range legs {
		dSecQty[dSec{lg.seller, lg.sec}] -= lg.qty
		dSecQty[dSec{lg.buyer, lg.sec}] += lg.qty
		dCash[lg.seller] += lg.cash
		dCash[lg.buyer] -= lg.cash
	}
	for k, delta := range dSecQty {
		if s.accounts[k.acct].securities[k.sec]+delta < 0 {
			panic("settlement: negative security position") // 判定逻辑保证不可达
		}
	}
	for a, delta := range dCash {
		if s.accounts[a].cash+delta < 0 {
			panic("settlement: negative cash position") // 判定逻辑保证不可达
		}
	}
	for k, delta := range dSecQty {
		s.accounts[k.acct].securities[k.sec] += delta
	}
	for a, delta := range dCash {
		s.accounts[a].cash += delta
	}

	s.overdue = nextOverdue
	delete(s.byDay, day)
	s.lastBatchDay = day
	s.lastReport = report
	return nil
}

// collectCandidates 合并滚动失败指令与当日到期指令，按编号升序。
// 开销只与当日待处理指令数相关：已了结指令不保留在任何候选结构中。
func (s *System) collectCandidates(day int64) []*instruction {
	due := s.byDay[day]
	out := make([]*instruction, 0, len(s.overdue)+len(due))
	i, j := 0, 0
	for i < len(s.overdue) || j < len(due) {
		if j >= len(due) || (i < len(s.overdue) && s.overdue[i].ID < due[j].ID) {
			out = append(out, s.overdue[i])
			i++
		} else {
			out = append(out, due[j])
			j++
		}
	}
	return out
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
