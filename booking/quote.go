package booking

func (s *System) refundQuoteLocked(id string, now int64) (RefundResult, error) {
	if id == "" || now < 0 {
		return RefundResult{}, errorf(ErrInvalidArgument, "invalid refund query")
	}
	ticket, ok := s.tickets[id]
	if !ok {
		return RefundResult{}, errorf(ErrTicketNotFound, "ticket %s", id)
	}
	if ticket.Refunded {
		return RefundResult{}, errorf(ErrTicketRefunded, "ticket %s", id)
	}
	result := RefundResult{TicketID: id, At: now, CurrentPrice: ticket.Price, PaidChangeFee: ticket.PaidChangeFee, Involuntary: ticket.Involuntary}
	if ticket.Involuntary {
		result.Refund = ticket.Price
		result.CashRefund = ticket.Price + ticket.PaidChangeFee
		result.Basis = "非自愿：全额退当前票面价，并现金退回历次已付改签费；既有代金券不收回"
		return result, nil
	}
	if now >= ticket.DepartAt {
		return RefundResult{}, errorf(ErrDeparted, "ticket %s", id)
	}
	currentTier := tier(ticket.DepartAt-now, s.cfg)
	result.Tier = currentTier.String()
	result.Rate = s.cfg.RefundRates.rate(currentTier)
	result.Fee = fee(ticket.Price, result.Rate)
	result.Refund = ticket.Price - result.Fee
	result.CashRefund = result.Refund
	result.Basis = "自愿：按原航班出发时刻计退票房档，应退=当前票面价-退票手续费，改签费不退"
	return result, nil
}

func (s *System) changeQuoteLocked(id, targetFlightID string, voucherID *string, now int64) (ChangeResult, error) {
	if id == "" || targetFlightID == "" || now < 0 || voucherID != nil && *voucherID == "" {
		return ChangeResult{}, errorf(ErrInvalidArgument, "invalid change query")
	}
	target := s.flights[targetFlightID]
	if target == nil || target.Canceled || targetFlightID == "" {
		return ChangeResult{}, errorf(ErrInvalidArgument, "target flight is unavailable")
	}
	ticket, ok := s.tickets[id]
	if !ok {
		return ChangeResult{}, errorf(ErrTicketNotFound, "ticket %s", id)
	}
	if ticket.Refunded {
		return ChangeResult{}, errorf(ErrTicketRefunded, "ticket %s", id)
	}
	if !ticket.Involuntary && now >= ticket.DepartAt {
		return ChangeResult{}, errorf(ErrDeparted, "ticket %s", id)
	}
	if !ticket.Involuntary && ticket.Changes >= s.cfg.ChangeLimit {
		return ChangeResult{}, errorf(ErrChangeLimit, "ticket %s used %d changes", id, ticket.Changes)
	}
	if targetFlightID == ticket.FlightID {
		return ChangeResult{}, errorf(ErrInvalidArgument, "target flight is unavailable")
	}

	result := ChangeResult{
		TicketID:        id,
		At:              now,
		PriceDifference: target.Price - ticket.Price,
		NewPrice:        target.Price,
		NewDepartAt:     target.DepartAt,
		Involuntary:     ticket.Involuntary,
		Changes:         ticket.Changes,
	}
	var selectedVoucher *Voucher
	if voucherID != nil {
		selectedVoucher = s.vouchers[*voucherID]
		if selectedVoucher == nil {
			return ChangeResult{}, errorf(ErrVoucherNotFound, "voucher %s", *voucherID)
		}
		if kind := selectedVoucher.usable(ticket.Owner, now); kind != "" {
			return ChangeResult{}, errorf(kind, "voucher %s", *voucherID)
		}
	}

	if ticket.Involuntary {
		result.Basis = "非自愿：免改签费；正差价免补，负差价不发现金券；改签后移除非自愿且不计次"
		return result, nil
	}
	currentTier := tier(ticket.DepartAt-now, s.cfg)
	result.Tier = currentTier.String()
	result.Rate = s.cfg.ChangeRates.rate(currentTier)
	result.ChangeFee = fee(ticket.Price, result.Rate)
	positiveDifference := result.PriceDifference
	if positiveDifference < 0 {
		positiveDifference = 0
		result.GeneratedVoucher = -result.PriceDifference
	}
	result.GrossDue = result.ChangeFee + positiveDifference
	if selectedVoucher != nil {
		result.VoucherDeduction = minInt64(selectedVoucher.Amount, result.GrossDue)
		if result.VoucherDeduction > 0 {
			result.VoucherID = selectedVoucher.ID
		}
	}
	result.CashDue = result.GrossDue - result.VoucherDeduction
	result.Basis = "自愿：改签费按原航班出发计档；正差价补付，负差价发等额代金券；券抵扣后现金须逐笔相等"
	return result, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
