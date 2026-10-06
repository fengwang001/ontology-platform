package ontology

// ChangeRequest 为改签请求。
// VoucherID 为空表示不使用代金券；Cash 为旅客提交的现金金额。
// 代金券与现金对自愿改签中的改签费及正差价均可抵扣。
type ChangeRequest struct {
	TicketID  string
	TargetID  string
	VoucherID string
	Cash      int64
}

// ChangeQuote 为改签金额结论（报价时不计实际支付，只算应付结构）。
type ChangeQuote struct {
	ChangeFee       int64 // 改签费（按原航班出发时刻计档；非自愿为 0）
	Diff            int64 // 目标票面价 - 当前票面价（可负）
	TotalDue        int64 // 应补总额 = 改签费 + 正差价（非自愿为 0）
	RefundAsVoucher int64 // 负差价时生成的代金券面额（自愿改签）
	CashDue         int64 // 使用代金券核减后的应补现金
	VoucherUsed     int64 // 本次核减的代金券面额
	Involuntary     bool
}

// ChangeResult 为改签成功结论。
type ChangeResult struct {
	ChangeQuote
	NewFare      int64
	NewDeparture int64
	NewFlightID  string
	VoucherID    string // 负差价时新生成的代金券
	Changes      int    // 改签后的自愿改签次数
}

type changeCalc struct {
	fee         int64
	diff        int64
	totalDue    int64
	newVoucher  int64 // 负差价应生成券面额
	involuntary bool
}

// calcChangeLocked 计算改签费用结构，不做支付校验、不改状态。
// 必须在已持锁状态下调用；t 未退，target 为有效未取消航班。
// 拒绝次序：已出发 优先于 改签次数超限；非自愿票两者均跳过。
func (s *System) prepareChangeLocked(t *Ticket, target *Flight, now int64) (changeCalc, error) {
	c := changeCalc{diff: target.Fare - t.Fare, involuntary: t.Involuntary}
	if t.Involuntary {
		// 非自愿改签：免改签费；正差价免补；负差价不退不生成券。
		return c, nil
	}
	tr := tierFor(t.Departure-now, s.cfg.LongThreshold, s.cfg.ShortThreshold)
	if tr.departed {
		return changeCalc{}, errf(KindDeparted, "ticket %s already departed at %d (now %d)", t.ID, t.Departure, now)
	}
	if t.Changes >= s.cfg.MaxChanges {
		return changeCalc{}, errf(KindChangeLimit, "ticket %s reached change limit %d", t.ID, s.cfg.MaxChanges)
	}
	c.fee = feeAt(t.Fare, percentAt(s.cfg.ChangePercents, tr.tier))
	c.totalDue = c.fee
	if c.diff > 0 {
		c.totalDue += c.diff
	} else if c.diff < 0 {
		c.newVoucher = -c.diff // 不退现金，等额生成归属购票人的代金券
	}
	return c, nil
}

// resolveTargetLocked 校验目标航班：参数不存在/已取消/与当前相同均归参数非法。
func (s *System) resolveTargetLocked(t *Ticket, targetID string) (*Flight, error) {
	if targetID == "" {
		return nil, errf(KindInvalidArgument, "empty target flight")
	}
	f := s.flights[targetID]
	if f == nil {
		return nil, errf(KindInvalidArgument, "target flight not found: %s", targetID)
	}
	if f.Cancelled {
		return nil, errf(KindInvalidArgument, "target flight cancelled: %s", targetID)
	}
	if f.ID == t.FlightID {
		return nil, errf(KindInvalidArgument, "target flight same as current: %s", targetID)
	}
	return f, nil
}

// QuoteChange 纯查询改签到某航班的应补金额（不指定支付手段时的结构）。
// 若提供 VoucherID，则按规则核减并给出应补现金。查询不推进时钟。
func (s *System) QuoteChange(req ChangeRequest, now int64) (ChangeQuote, error) {
	if req.TicketID == "" || req.TargetID == "" || now < 0 || req.Cash < 0 {
		return ChangeQuote{}, errf(KindInvalidArgument, "invalid change args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return ChangeQuote{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tickets[req.TicketID]
	if t == nil {
		return ChangeQuote{}, errf(KindTicketNotFound, "ticket not found: %s", req.TicketID)
	}
	if t.Refunded {
		return ChangeQuote{}, errf(KindTicketState, "ticket %s already refunded", req.TicketID)
	}
	target, err := s.resolveTargetLocked(t, req.TargetID)
	if err != nil {
		return ChangeQuote{}, err
	}
	c, err := s.prepareChangeLocked(t, target, now)
	if err != nil {
		return ChangeQuote{}, err
	}
	q := ChangeQuote{
		ChangeFee:       c.fee,
		Diff:            c.diff,
		TotalDue:        c.totalDue,
		RefundAsVoucher: c.newVoucher,
		Involuntary:     c.involuntary,
	}
	if c.totalDue > 0 && req.VoucherID != "" {
		v, err := s.validateVoucher(req.VoucherID, t.Owner, now)
		if err != nil {
			return ChangeQuote{}, err
		}
		q.VoucherUsed = min64(v.Amount, c.totalDue)
	}
	q.CashDue = c.totalDue - q.VoucherUsed
	return q, nil
}

// Change 执行改签。任何校验被拒绝都不推进时钟、不改变票与代金券。
func (s *System) Change(req ChangeRequest, now int64) (ChangeResult, error) {
	if req.TicketID == "" || req.TargetID == "" || now < 0 || req.Cash < 0 {
		return ChangeResult{}, errf(KindInvalidArgument, "invalid change args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return ChangeResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClockLocked(now); err != nil {
		return ChangeResult{}, err
	}
	t := s.tickets[req.TicketID]
	if t == nil {
		return ChangeResult{}, errf(KindTicketNotFound, "ticket not found: %s", req.TicketID)
	}
	if t.Refunded {
		return ChangeResult{}, errf(KindTicketState, "ticket %s already refunded", req.TicketID)
	}
	target, err := s.resolveTargetLocked(t, req.TargetID)
	if err != nil {
		return ChangeResult{}, err
	}
	c, err := s.prepareChangeLocked(t, target, now)
	if err != nil {
		return ChangeResult{}, err
	}

	// 支付结算。非自愿改签应补总额为 0：提交现金或代金券均按支付不符拒绝。
	var voucher *Voucher
	voucherUsed := int64(0)
	if c.totalDue > 0 {
		if req.VoucherID != "" {
			voucher, err = s.validateVoucher(req.VoucherID, t.Owner, now)
			if err != nil {
				return ChangeResult{}, err
			}
			voucherUsed = min64(voucher.Amount, c.totalDue)
		}
	} else if req.VoucherID != "" || req.Cash != 0 {
		return ChangeResult{}, paymentMismatch(req.Cash, 0)
	}
	cashDue := c.totalDue - voucherUsed
	if req.Cash != cashDue {
		return ChangeResult{}, paymentMismatch(req.Cash, cashDue)
	}

	// 全部校验通过后才推进时钟并落地变更。
	s.now = now
	if voucher != nil {
		consumeVoucher(voucher, voucherUsed)
	}
	newVID := ""
	if c.newVoucher > 0 {
		nv := s.createVoucher(t.Owner, c.newVoucher, now)
		newVID = nv.ID
	}
	t.Fare = target.Fare
	t.Departure = target.Departure
	t.FlightID = target.ID
	wasInvoluntary := t.Involuntary
	t.Involuntary = false // 非自愿改签后不再带非自愿标记
	if !wasInvoluntary {
		t.Changes++
		t.ChangeFees += c.fee
		t.CashPaid += cashDue
	}
	return ChangeResult{
		ChangeQuote: ChangeQuote{
			ChangeFee:       c.fee,
			Diff:            c.diff,
			TotalDue:        c.totalDue,
			RefundAsVoucher: c.newVoucher,
			CashDue:         cashDue,
			VoucherUsed:     voucherUsed,
			Involuntary:     wasInvoluntary,
		},
		NewFare:      target.Fare,
		NewDeparture: target.Departure,
		NewFlightID:  target.ID,
		VoucherID:    newVID,
		Changes:      t.Changes,
	}, nil
}

func paymentMismatch(paid, due int64) error {
	return &OpError{
		Kind:     KindPaymentMismatch,
		Message:  "cash amount does not match cash due",
		CashDue:  due,
		Expected: paid,
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
