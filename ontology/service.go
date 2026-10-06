package ontology

import "sort"

const landlordPayer = int64(-1) // 账单由房东代收

// checkClock 校验时钟单调；返回是否通过。
func (s *Service) checkClock(now int64) bool {
	return s.clockInit && now < s.lastNow
}

// CheckIn 登记一名住户入住 [checkInDay 起开放) 的某房间。
// area 为该房间面积（按面积分摊时使用），同名房间重复登记时面积必须一致。
func (s *Service) CheckIn(now, id, room, checkInDay, area int64) error {
	// 1) 参数非法
	if now < 0 || id <= 0 || room <= 0 || checkInDay < 0 || checkInDay > now || area <= 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 2) 时钟回退
	if s.checkClock(now) {
		return ErrClockBack
	}
	// 3) 不存在不适用；重复 ID 视为参数非法在参数阶段无法判，放状态之前
	if _, dup := s.residents[id]; dup {
		return ErrInvalid
	}
	if old, ok := s.roomAreas[room]; ok && old != area {
		return ErrInvalid
	}
	// 4) 在住期重叠：与该房间既有各段比较
	for _, r := range s.residents {
		for _, seg := range r.Segments {
			if seg.Room != room {
				continue
			}
			if segmentsOverlap(checkInDay, 0, seg.Start, seg.End) {
				return ErrOverlap
			}
		}
	}
	// 5) 状态不允许 / 6) 金额越界：本操作不涉及

	s.roomAreas[room] = area
	s.residents[id] = &Resident{
		ID:       id,
		Segments: []Segment{{ResidentID: id, Room: room, Start: checkInDay, End: 0}},
		Active:   true,
	}
	s.reattributeAll()
	s.lastNow, s.clockInit = now, true
	return nil
}

// CheckOut 办理退出：在 outDay 关闭在住期并执行退出清算。
func (s *Service) CheckOut(now, id, outDay int64) error {
	if now < 0 || id <= 0 || outDay < 0 || outDay > now {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkClock(now) {
		return ErrClockBack
	}
	r, ok := s.residents[id]
	if !ok {
		return ErrNotFound
	}
	last := r.Segments[len(r.Segments)-1]
	if outDay < last.Start {
		return ErrInvalid
	}
	// 已退出再退出属于状态不允许
	if !r.Active {
		return ErrState
	}

	// 退出前：对覆盖 outDay 且仍 active 的账单按退出后在住状态重算归属，
	// 使“退出日当天起不在住、退出日后账单不再分给他”，并把增量留给在住住户对。
	s.reattributeOnCheckout(id, outDay)
	r.Segments[len(r.Segments)-1].End = outDay
	r.Active = false
	r.OutDay = outDay
	s.seq++
	s.outSeq[id] = s.seq
	lines := s.foldAtSettlement(id)
	s.settled[id] = true
	s.appendSettlement(id, Settlement{
		ResidentID: id,
		AtDay:      outDay,
		Suppl:      false,
		Reason:     "checkout",
		Lines:      lines,
	})
	s.lastNow = now
	return nil
}

// ChangeRoom 在 day 当天把住户换到 newRoom，换房日起按新房间面积分摊。
func (s *Service) ChangeRoom(now, id, newRoom, day, area int64) error {
	if now < 0 || id <= 0 || newRoom <= 0 || day < 0 || day > now || area <= 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkClock(now) {
		return ErrClockBack
	}
	r, ok := s.residents[id]
	if !ok {
		return ErrNotFound
	}
	if old, exists := s.roomAreas[newRoom]; exists && old != area {
		return ErrInvalid
	}
	cur := r.Segments[len(r.Segments)-1]
	if day < cur.Start || (!r.Active && day >= cur.End) {
		return ErrInvalid
	}
	// 与新房间既有在住期比较（本住户当前段除外）
	for _, other := range s.residents {
		if other.ID == id {
			continue
		}
		for _, seg := range other.Segments {
			if seg.Room != newRoom {
				continue
			}
			if segmentsOverlap(day, 0, seg.Start, seg.End) {
				return ErrOverlap
			}
		}
	}
	if !r.Active {
		return ErrState
	}
	if cur.Room == newRoom {
		return ErrState
	}

	s.roomAreas[newRoom] = area
	r.Segments[len(r.Segments)-1].End = day
	r.Segments = append(r.Segments, Segment{ResidentID: id, Room: newRoom, Start: day, End: 0})
	s.reattributeAll()
	s.lastNow = now
	return nil
}

// EnterBill 录入一张共同账单，amount 为正整数，覆盖 [startDay, endDay)。
// payerID 为垫付住户 ID；landlordCollect 为 true 时由房东代收。
func (s *Service) EnterBill(now, id, amount int64, startDay, endDay int64, method SplitMethod, payerID int64, landlordCollect bool) error {
	if now < 0 || id <= 0 || amount <= 0 || startDay < 0 || endDay <= startDay ||
		(method != SplitPerHead && method != SplitByArea) || (!landlordCollect && payerID <= 0) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkClock(now) {
		return ErrClockBack
	}
	if _, dup := s.bills[id]; dup {
		return ErrInvalid
	}
	if !landlordCollect && !s.inResident(payerID) {
		return ErrNotFound
	}
	if amount > s.maxAmount {
		return ErrAmount
	}

	payer := payerID
	if landlordCollect {
		payer = landlordPayer
	}
	cs := attribute(attributionInput{
		amount:   amount,
		startDay: startDay,
		endDay:   endDay,
		method:   method,
	}, s.residents, s.roomAreas)
	b := &Bill{
		ID:       id,
		Amount:   amount,
		StartDay: startDay,
		EndDay:   endDay,
		Method:   method,
		PayerID:  payer,
		EntryDay: now,
		Status:   BillActive,
		Contribs: cs,
	}
	s.seq++
	b.Seq = s.seq
	s.bills[id] = b
	s.addNet(cs, payer, id, 1)
	s.foldNewBill(b, now)
	s.lastNow = now
	return nil
}

// RaiseDispute 在账单录入后 D 天内提出争议（每账单仅一次），账单立即从净额剔除。
func (s *Service) RaiseDispute(now, billID int64) error {
	if now < 0 || billID <= 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkClock(now) {
		return ErrClockBack
	}
	b, ok := s.bills[billID]
	if !ok {
		return ErrNotFound
	}
	if b.Status == BillAdjudicated || b.Disputed {
		return ErrState
	}
	if now > b.EntryDay+s.disputeWindow {
		return ErrState
	}

	b.OrigContribs = b.Contribs
	b.OrigAmount = b.Amount
	s.addNet(b.Contribs, b.PayerID, b.ID, -1)
	b.Status = BillDisputed
	b.Disputed = true
	s.lastNow = now
	return nil
}

// Adjudicate 裁定争议账单，newAmount 不得高于原金额，并按新金额重新归属。
func (s *Service) Adjudicate(now, billID, newAmount int64) error {
	if now < 0 || billID <= 0 || newAmount < 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkClock(now) {
		return ErrClockBack
	}
	b, ok := s.bills[billID]
	if !ok {
		return ErrNotFound
	}
	if b.Status != BillDisputed {
		return ErrState
	}
	if newAmount > b.Amount {
		return ErrAmount
	}

	newCS := attribute(attributionInput{
		amount:   newAmount,
		startDay: b.StartDay,
		endDay:   b.EndDay,
		method:   b.Method,
	}, s.residents, s.roomAreas)
	oldCS, oldAmount := b.OrigContribs, b.OrigAmount
	b.Status = BillAdjudicated
	b.Adjudication = newAmount
	b.Contribs = newCS
	b.Amount = newAmount
	s.addNet(newCS, b.PayerID, b.ID, 1)
	s.adjudicateFolded(b, oldCS, oldAmount, newCS, newAmount, now)
	s.lastNow = now
	return nil
}

// NetBetween 返回两名住户之间的当前净额：正表示规范方向上较小 ID 一方应收，
// 查询仅为一次 map 读取，开销与账单总数无关。
func (s *Service) NetBetween(a, b int64) (int64, error) {
	if a <= 0 || b <= 0 || a == b {
		return 0, ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.inResident(a) || !s.inResident(b) {
		return 0, ErrNotFound
	}
	return s.net[canonPair(a, b)], nil
}

// Settlements 返回某住户的全部清算记录（原清算 + 各次补充清算）副本。
func (s *Service) Settlements(id int64) ([]Settlement, error) {
	if id <= 0 {
		return nil, ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.inResident(id) {
		return nil, ErrNotFound
	}
	src := s.settlements[id]
	out := make([]Settlement, len(src))
	copy(out, src)
	return out, nil
}

// LandlordShare 返回某账单中由房东承担的份额（守恒校验用）。
func (s *Service) LandlordShare(billID int64) (int64, error) {
	if billID <= 0 {
		return 0, ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bills[billID]
	if !ok {
		return 0, ErrNotFound
	}
	return landlordContrib(b.Contribs), nil
}

// sumDirected 对所有当前生效账单重算各住户有向头寸之和（含房东账单为 0），恒为 0。
func sumDirected(s *Service) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, b := range s.bills {
		if b.Status == BillDisputed || b.PayerID == landlordPayer {
			continue
		}
		for _, c := range b.Contribs {
			if c.ResidentID == 0 {
				continue
			}
			sum += signedContrib(c.Amount, b.Amount, b.PayerID, c.ResidentID)
		}
	}
	return sum
}

// billContribCopy 返回账单当前归属副本，测试用。
func (s *Service) billContribCopy(billID int64) []Contribution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.bills[billID]
	out := make([]Contribution, len(b.Contribs))
	copy(out, b.Contribs)
	sort.Slice(out, func(i, j int) bool { return out[i].ResidentID < out[j].ResidentID })
	return out
}
