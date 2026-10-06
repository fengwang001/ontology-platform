package sla

// Ruling 是一笔订单裁决的只读结果。
type Ruling struct {
	OrderID         string
	Delivered       bool
	Canceled        bool
	Adjudicated     bool
	Automatic       bool
	PromisedAt      int64 // 原始承诺时刻
	ExtendedPromise int64 // 延展后承诺时刻（截断后）
	Delay           int64
	Payout          int64
	Party           Party
	MerchantBlame   int64
	RiderBlame      int64
	PlatformBlame   int64
	UserBlame       int64
}

// Claim 处理用户赔付申请。拒绝次序：
// 参数 → 不存在 → 已取消 → 已赔付 → 未送达 → 窗口外 → 无延误。
func (s *System) Claim(orderID string, at int64) (*Ruling, error) {
	if at < 0 {
		return nil, errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil, errf(CodeOrderNotFound, "order not found")
	}
	if o.canceled {
		return nil, errf(CodeOrderCanceled, "order canceled")
	}
	if o.adjudicated {
		return nil, errf(CodeAlreadyPaid, "already adjudicated")
	}
	if o.stage != stageDelivered {
		return nil, errf(CodeNotDelivered, "order not delivered")
	}
	deadline, _ := addInt64(o.deliverAt, o.claimWindow)
	if at < o.deliverAt || at >= deadline { // 恰等于右端点不允许
		return nil, errf(CodeWindowClosed, "claim outside window")
	}
	delay := o.deliverAt - o.extendedPromise()
	if delay <= 0 {
		return nil, errf(CodeNoDelay, "no delay")
	}
	entry := s.adjudicate(o, at, false)
	return rulingOf(o, entry), nil
}

// AutoAdjudicate 对单笔订单在窗口结束后自动裁决；
// 仅未申请、延误落入最高档的订单可被自动裁决。与用户申请互斥。
func (s *System) AutoAdjudicate(orderID string, at int64) (*Ruling, error) {
	if at < 0 {
		return nil, errf(CodeInvalidParam, "negative timestamp")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil, errf(CodeOrderNotFound, "order not found")
	}
	if o.canceled {
		return nil, errf(CodeOrderCanceled, "order canceled")
	}
	if o.adjudicated {
		return nil, errf(CodeAlreadyPaid, "already adjudicated")
	}
	if o.stage != stageDelivered {
		return nil, errf(CodeNotDelivered, "order not delivered")
	}
	deadline, _ := addInt64(o.deliverAt, o.claimWindow)
	if at < deadline {
		return nil, errf(CodeWindowClosed, "claim window still open")
	}
	if !o.autoCandidate {
		return nil, errf(CodeInvalidParam, "order not eligible for auto adjudication")
	}
	entry := s.adjudicate(o, at, true)
	return rulingOf(o, entry), nil
}

// adjudicate 在调用方已持锁、订单已送达的前提下作出不可更改的裁决。
func (s *System) adjudicate(o *order, at int64, automatic bool) *LedgerEntry {
	extended := o.extendedPromise()
	delay := o.deliverAt - extended
	blame := attribute(o, extended)
	party := blame.chooseParty()
	amount := int64(0)
	if party != PartyUser { // 用户为责任方时不赔付
		if payout, ok := tierPayout(o.tiers, delay); ok {
			amount = payout
		}
	}
	entry := &LedgerEntry{
		OrderID:   o.id,
		At:        at,
		Amount:    amount,
		Delay:     delay,
		Party:     party,
		Automatic: automatic,
	}
	o.adjudicated = true
	o.autoCandidate = false
	o.entry = entry
	s.ledger = append(s.ledger, *entry)
	return entry
}

// tierPayout 按延误查找档位赔付额；取等归高档，超过最高阈值归最高档。
// 返回 (金额, 是否落入某档)。delay 低于最低阈值时 ok=false。
func tierPayout(tiers []Tier, delay int64) (int64, bool) {
	idx := -1
	for i, t := range tiers {
		if delay >= t.Threshold { // 取等归入该阈值对应档（高档优先）
			idx = i
		} else {
			break
		}
	}
	if idx < 0 {
		return 0, false
	}
	return tiers[idx].Payout, true
}

// RulingOf 查询订单当前裁决与归因明细（只读快照）。
func (s *System) RulingOf(orderID string) (*Ruling, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil, errf(CodeOrderNotFound, "order not found")
	}
	var entry *LedgerEntry
	if o.adjudicated {
		entry = o.entry
	}
	return rulingOf(o, entry), nil
}

func rulingOf(o *order, entry *LedgerEntry) *Ruling {
	r := &Ruling{
		OrderID:         o.id,
		Delivered:       o.stage == stageDelivered,
		Canceled:        o.canceled,
		Adjudicated:     o.adjudicated,
		PromisedAt:      o.promisedAt,
		ExtendedPromise: o.extendedPromise(),
	}
	if o.stage == stageDelivered {
		r.Delay = o.deliverAt - r.ExtendedPromise
		blame := attribute(o, r.ExtendedPromise)
		r.MerchantBlame, r.RiderBlame = blame.merchant, blame.rider
		r.PlatformBlame, r.UserBlame = blame.platform, blame.user
	}
	if entry != nil {
		r.Automatic = entry.Automatic
		r.Payout = entry.Amount
		r.Party = entry.Party
		r.Delay = entry.Delay
	}
	return r
}

// Ledger 返回已生效裁决账目的确定性顺序副本（按裁决发生次序）。
func (s *System) Ledger() []LedgerEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LedgerEntry, len(s.ledger))
	copy(out, s.ledger)
	return out
}
