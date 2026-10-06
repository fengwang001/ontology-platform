package delivery

// applyUndeliverable 将无法送达按商家预设推进到处置流，并保证处置补偿每单至多一次。
// closeStatus 标记异常的关闭原因（到期或判定）。
// 调用方须已通过全部拒绝检查并推进时钟。
func (s *System) applyUndeliverable(o *order, ex *exception, at int64, closeStatus ExceptionStatus) {
	ex.view.Status = closeStatus // ESClosedExpired（到期）或 ESUndeliverable（判定）
	o.view.ActiveException = ""
	// 三类无法送达（联系不上/拒收/地址未纠正）责任均归用户；
	// 仅退回未确认会在后续惰性到期时改写为商家。
	o.view.Responsibility = PartyUser
	if !o.view.CompPaid {
		o.view.CompPaid = true
		o.view.CompAmount = s.params.RiderCompensation
	}
	if o.view.Disposition == DispLocal {
		o.view.Status = OSHandled
		return
	}
	o.view.Status = OSReturning
}

func (s *System) RiderReturn(orderID string, at int64) error {
	if orderID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid return args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + orderID}
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.settleOrderAt(o, at)
	switch o.view.Status {
	case OSReturnUnconfirmed:
		return &OpError{Code: ErrConfirmWindow, Msg: "merchant confirm window expired"}
	case OSReturning:
		if o.view.ReturnedAt >= 0 {
			return &OpError{Code: ErrWrongState, Msg: "already returned to merchant"}
		}
		s.commit(at)
		o.view.ReturnedAt = at
		o.view.ConfirmDeadline = at + s.params.MerchantConfirmWin
		return nil
	default:
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "order terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "order is not returning"}
	}
}

func (s *System) MerchantConfirm(orderID string, at int64) error {
	if orderID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid confirm args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + orderID}
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.settleAt(o, at)
	if o.view.Status == OSReturnUnconfirmed {
		return &OpError{Code: ErrConfirmWindow, Msg: "merchant confirm window expired"}
	}
	if o.view.Status != OSReturning {
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "order terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "order is not returning"}
	}
	if o.view.ReturnedAt < 0 {
		return &OpError{Code: ErrWrongState, Msg: "rider has not returned the order"}
	}
	if at >= o.view.ConfirmDeadline {
		return &OpError{Code: ErrConfirmWindow, Msg: "merchant confirm at/after window right edge"}
	}
	s.commit(at)
	o.view.Status = OSReturned
	return nil
}
