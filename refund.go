package ontology

// RefundQuote 为退票报价；手续费档基于当前航班出发时刻。
type RefundQuote struct {
	RefundCash  int64 // 应退现金（分）
	Fare        int64 // 当前票面价
	Fee         int64 // 退票手续费（自愿退票时）
	ChangeFees  int64 // 额外退回的历次改签费（仅非自愿退票）
	Involuntary bool
}

// RefundResult 为退票成功后的结论。
type RefundResult struct {
	RefundCash  int64
	Fare        int64
	Fee         int64
	ChangeFees  int64
	Involuntary bool
}

// quoteRefundLocked 计算退票结论，不推进时钟、不改状态。
// 必须在已持锁状态下调用，t 保证存在且未退。
func (s *System) quoteRefundLocked(t *Ticket, now int64) (RefundQuote, error) {
	q := RefundQuote{Fare: t.Fare, Involuntary: t.Involuntary}
	if t.Involuntary {
		// 非自愿：全额退回当前票面价，并以现金额外退回历次已付改签费。
		q.ChangeFees = t.ChangeFees
		q.RefundCash = t.Fare + t.ChangeFees
		return q, nil
	}
	tr := tierFor(t.Departure-now, s.cfg.LongThreshold, s.cfg.ShortThreshold)
	if tr.departed {
		return RefundQuote{}, errf(KindDeparted, "ticket %s already departed at %d (now %d)", t.ID, t.Departure, now)
	}
	q.Fee = feeAt(t.Fare, percentAt(s.cfg.RefundPercents, tr.tier))
	q.RefundCash = t.Fare - q.Fee
	return q, nil
}

// QuoteRefund 纯查询此刻退票应退金额：O(1)，不随改签次数/票总数增长。
// 查询不推进时钟，因此不检查时钟回退；其余拒绝次序与退票一致。
func (s *System) QuoteRefund(ticketID string, now int64) (RefundQuote, error) {
	if ticketID == "" || now < 0 {
		return RefundQuote{}, errf(KindInvalidArgument, "invalid refund args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return RefundQuote{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tickets[ticketID]
	if t == nil {
		return RefundQuote{}, errf(KindTicketNotFound, "ticket not found: %s", ticketID)
	}
	if t.Refunded {
		return RefundQuote{}, errf(KindTicketState, "ticket %s already refunded", ticketID)
	}
	return s.quoteRefundLocked(t, now)
}

// Refund 执行退票。退票后票进入已退状态，不可再改签或退票。
func (s *System) Refund(ticketID string, now int64) (RefundResult, error) {
	if ticketID == "" || now < 0 {
		return RefundResult{}, errf(KindInvalidArgument, "invalid refund args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return RefundResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return RefundResult{}, err
	}
	t := s.tickets[ticketID]
	if t == nil {
		return RefundResult{}, errf(KindTicketNotFound, "ticket not found: %s", ticketID)
	}
	if t.Refunded {
		return RefundResult{}, errf(KindTicketState, "ticket %s already refunded", ticketID)
	}
	q, err := s.quoteRefundLocked(t, now)
	if err != nil {
		return RefundResult{}, err
	}
	s.now = now
	t.Refunded = true
	t.CashRefunded += q.RefundCash
	return RefundResult{
		RefundCash:  q.RefundCash,
		Fare:        q.Fare,
		Fee:         q.Fee,
		ChangeFees:  q.ChangeFees,
		Involuntary: q.Involuntary,
	}, nil
}
