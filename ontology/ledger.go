package ontology

import "sort"

// pairKey 是一对住户的无向规范键（较小 ID 在前）。
type pairKey struct{ a, b int64 }

func canonPair(x, y int64) pairKey {
	if x > y {
		x, y = y, x
	}
	return pairKey{x, y}
}

// orientSign 返回 resident 视角下规范键净额的方向系数。
// N>0 表示 a 应收 b；resident 为 a 取 +1，为 b 取 -1。
func orientSign(k pairKey, resident int64) int64 {
	if k.a == resident {
		return 1
	}
	return -1
}

// signedContrib 是一张账单对某住户的有向贡献：正为应收，负为应付（房东账单为 0）。
func signedContrib(share, billAmount, payerID, residentID int64) int64 {
	v := -share
	if payerID == residentID {
		v += billAmount
	}
	return v
}

func shareOf(cs []Contribution, id int64) int64 {
	for _, c := range cs {
		if c.ResidentID == id {
			return c.Amount
		}
	}
	return 0
}

func (s *Service) foldedParty(id, billID int64) bool {
	inc := s.included[id]
	return inc != nil && inc[billID]
}

func (s *Service) markIncluded(id, billID int64) {
	m := s.included[id]
	if m == nil {
		m = map[int64]bool{}
		s.included[id] = m
	}
	m[billID] = true
}

// addNet 把一张账单按归属结果写入当前净额，只写双方均在住（未清算）的住户对。
// dir=+1 计入，dir=-1 剔除（争议）。
func (s *Service) addNet(cs []Contribution, payer int64, billID int64, dir int64) {
	if payer == landlordPayer {
		return
	}
	for _, c := range cs {
		if c.ResidentID == 0 || c.ResidentID == payer || c.Amount == 0 {
			continue
		}
		if s.settled[payer] || s.settled[c.ResidentID] {
			continue
		}
		k := canonPair(payer, c.ResidentID)
		d := int64(1)
		if k.a != payer {
			d = -1
		}
		s.net[k] += dir * d * c.Amount
	}
}

// ensureFolded 惰性初始化某住户的清算头寸表。
func (s *Service) ensureFolded(id int64) map[int64]int64 {
	m := s.folded[id]
	if m == nil {
		m = map[int64]int64{}
		s.folded[id] = m
	}
	return m
}

// reattributeOnCheckout 在某住户退出时，用“其已退出”后的在住状态重算所有
// active 且覆盖 outDay 的账单归属，并把净额头寸增量只应用到双方仍在住的住户对。
// 退出住户本人的新旧有向贡献之差随后进入其原清算（foldAtSettlement 直接读取新快照）。
func (s *Service) reattributeOnCheckout(id, outDay int64) {
	// 先临时把该住户置为退出态，attribute 即按退出后历史计算。
	r := s.residents[id]
	r.Segments[len(r.Segments)-1].End = outDay
	r.Active = false
	r.OutDay = outDay
	for bid, b := range s.bills {
		if b.Status != BillActive || shareOf(b.Contribs, id) == 0 {
			continue
		}
		oldCS := b.Contribs
		newCS := attribute(attributionInput{
			amount:   b.Amount,
			startDay: b.StartDay,
			endDay:   b.EndDay,
			method:   b.Method,
		}, s.residents, s.roomAreas)
		b.Contribs = newCS
		// 对双方都不是退出者本人的住户对，应用新旧差额（仍留在当前净额）。
		deltaPair := map[pairKey]int64{}
		collect := func(cs []Contribution, sign int64) {
			if b.PayerID == landlordPayer {
				return
			}
			for _, c := range cs {
				if c.ResidentID == 0 || c.ResidentID == b.PayerID {
					continue
				}
				if c.ResidentID == id || b.PayerID == id {
					continue
				}
				if s.settled[c.ResidentID] || s.settled[b.PayerID] {
					continue
				}
				k := canonPair(b.PayerID, c.ResidentID)
				d := int64(1)
				if k.a != b.PayerID {
					d = -1
				}
				deltaPair[k] += sign * d * c.Amount
			}
		}
		collect(oldCS, -1)
		collect(newCS, 1)
		for k, d := range deltaPair {
			s.net[k] += d
		}
		_ = bid
	}
}

// reattributeAll 用当前在住历史重算所有 active 账单归属，
// 把“双方均未退出”住户对的净额头寸按新旧归属差额增量更新。
// 用于入住/换房后：在住集合或房间面积发生变化，逐日归属随之改变。
func (s *Service) reattributeAll() {
	for _, b := range s.bills {
		if b.Status != BillActive {
			continue
		}
		oldCS := b.Contribs
		newCS := attribute(attributionInput{
			amount:   b.Amount,
			startDay: b.StartDay,
			endDay:   b.EndDay,
			method:   b.Method,
		}, s.residents, s.roomAreas)
		b.Contribs = newCS
		if b.PayerID == landlordPayer {
			continue
		}
		delta := map[pairKey]int64{}
		add := func(cs []Contribution, sign int64) {
			for _, c := range cs {
				if c.ResidentID == 0 || c.ResidentID == b.PayerID || c.Amount == 0 {
					continue
				}
				if s.settled[b.PayerID] || s.settled[c.ResidentID] {
					continue
				}
				k := canonPair(b.PayerID, c.ResidentID)
				d := int64(1)
				if k.a != b.PayerID {
					d = -1
				}
				delta[k] += sign * d * c.Amount
			}
		}
		add(oldCS, -1)
		add(newCS, 1)
		for k, v := range delta {
			s.net[k] += v
		}
	}
}

// foldAtSettlement 执行退出清算：摘取该住户在当前净额中的全部头寸，
// 逐账单登记并入标记，清零净额，返回原清算各行（resident 视角有向金额）。
func (s *Service) foldAtSettlement(id int64) []SettlementLine {
	fold := s.ensureFolded(id)
	// 按账单计算该住户在退出时点的全部有向贡献。
	// active 账单、且录入不晚于退出日的，参与原清算；争议中的账单不参与。
	byOther := map[int64]int64{}
	for bid, b := range s.bills {
		if b.Status != BillActive || b.PayerID == landlordPayer || b.Seq == 0 {
			continue
		}
		if b.Seq >= s.outSeq[id] {
			continue
		}
		cs := b.Contribs
		if b.PayerID == id {
			hasReceivable := false
			for _, c := range cs {
				if c.ResidentID != 0 && c.ResidentID != id && c.Amount > 0 {
					byOther[c.ResidentID] += c.Amount
					hasReceivable = true
				}
			}
			if hasReceivable {
				s.markIncluded(id, bid)
			}
			continue
		}
		if s.foldedParty(id, bid) {
			continue
		}
		if d := -shareOf(cs, id); d != 0 {
			byOther[b.PayerID] += d
			s.markIncluded(id, bid)
		}
	}

	// 双方均在住的部分当前在 net 中：用账单口径替换，摘取后清零。
	for k := range s.net {
		if k.a == id || k.b == id {
			s.net[k] = 0
		}
	}
	lines := []SettlementLine{}
	for other, d := range byOther {
		fold[other] += d
		lines = append(lines, SettlementLine{OtherID: other, Amount: d})
	}
	for other := range s.residents {
		if other != id {
			if _, ok := byOther[other]; ok {
				continue
			}
			lines = append(lines, SettlementLine{OtherID: other, Amount: 0})
		}
	}
	sortLines(lines)
	return lines
}

// foldNewBill 把一张新账单中涉及已退出住户的部分并入各自清算头寸，
// 清零对应净额（addNet 未写入这些对，故净额本为 0），生成补充清算行。
func (s *Service) foldNewBill(b *Bill, now int64) {
	if b.PayerID == landlordPayer {
		return
	}
	suppl := map[int64][]SettlementLine{}
	addLine := func(resident, other, amount int64) {
		s.ensureFolded(resident)[other] += amount
		suppl[resident] = append(suppl[resident], SettlementLine{OtherID: other, Amount: amount})
	}
	for _, c := range b.Contribs {
		if c.ResidentID == 0 || c.ResidentID == b.PayerID || c.Amount == 0 {
			continue
		}
		// 只补“退出日早于本账单录入日”的住户；其原清算不可能包含此账单。
		if s.settled[c.ResidentID] && s.outSeq[c.ResidentID] < b.Seq {
			s.markIncluded(c.ResidentID, b.ID)
			addLine(c.ResidentID, b.PayerID, -c.Amount)
		}
	}
	// 垫付人退出清算早于录账：对每个承担住户分别补应收。
	if s.settled[b.PayerID] && s.outSeq[b.PayerID] < b.Seq {
		s.markIncluded(b.PayerID, b.ID)
		for _, c := range b.Contribs {
			sharee := c.ResidentID
			if sharee == 0 || sharee == b.PayerID || c.Amount == 0 {
				continue
			}
			if !s.settled[sharee] || s.outSeq[sharee] < b.Seq {
				addLine(b.PayerID, c.ResidentID, c.Amount)
			}
		}
	}
	for id, lines := range suppl {
		sortLines(lines)
		s.appendSettlement(id, Settlement{
			ResidentID: id, AtDay: now, Suppl: true,
			Reason: "supplement:new-bill", Lines: lines,
		})
	}
}

// adjudicateFolded 裁定后对已清算住户：按新旧有向贡献之差调整头寸并生成补充清算。
func (s *Service) adjudicateFolded(b *Bill, oldCS []Contribution, oldAmount int64, newCS []Contribution, newAmount, now int64) {
	if b.PayerID == landlordPayer {
		return
	}
	suppl := map[int64][]SettlementLine{}
	addLine := func(resident, other, amount int64) {
		s.ensureFolded(resident)[other] += amount
		suppl[resident] = append(suppl[resident], SettlementLine{OtherID: other, Amount: amount})
	}
	// 候选：已标记并入的住户，以及争议期间才退出（无标记）但裁定结果中承担份额的住户。
	cand := map[int64]bool{}
	for id, inc := range s.included {
		if inc[b.ID] {
			cand[id] = true
		}
	}
	for _, c := range newCS {
		if c.ResidentID != 0 && c.ResidentID != b.PayerID && s.settled[c.ResidentID] {
			cand[c.ResidentID] = true
		}
	}
	if s.settled[b.PayerID] {
		cand[b.PayerID] = true
	}
	for id := range cand {
		marked := s.foldedParty(id, b.ID)
		_ = marked
		if id == b.PayerID {
			// 垫付人已退出：与每个承担份额发生变化的住户分别成行（新旧承担者并集）。
			sharees := map[int64]bool{}
			for _, c := range newCS {
				if c.ResidentID != 0 && c.ResidentID != id {
					sharees[c.ResidentID] = true
				}
			}
			for _, c := range oldCS {
				if c.ResidentID != 0 && c.ResidentID != id {
					sharees[c.ResidentID] = true
				}
			}
			for other := range sharees {
				oldShare := int64(0)
				if s.foldedParty(id, b.ID) {
					oldShare = shareOf(oldCS, other)
				}
				d := shareOf(newCS, other) - oldShare
				if d != 0 {
					addLine(id, other, d)
				}
			}
			continue
		}
		oldD := int64(0)
		if s.foldedParty(id, b.ID) {
			oldD = signedContrib(shareOf(oldCS, id), oldAmount, b.PayerID, id)
		}
		newD := signedContrib(shareOf(newCS, id), newAmount, b.PayerID, id)
		if d := newD - oldD; d != 0 {
			addLine(id, b.PayerID, d)
		}
		s.markIncluded(id, b.ID)
	}
	if cand[b.PayerID] {
		s.markIncluded(b.PayerID, b.ID)
	}
	for id, lines := range suppl {
		sortLines(lines)
		s.appendSettlement(id, Settlement{
			ResidentID: id, AtDay: now, Suppl: true,
			Reason: "supplement:adjudication", Lines: lines,
		})
	}
}

func (s *Service) appendSettlement(id int64, rec Settlement) {
	s.settlements[id] = append(s.settlements[id], rec)
}

func sortLines(lines []SettlementLine) {
	sort.Slice(lines, func(i, j int) bool { return lines[i].OtherID < lines[j].OtherID })
}
