package threematch

// Pay 对已放行发票付款。
//
// 折扣规则：payAt <= releasedAt + DiscountPeriodSec（含恰等）时享受
// 折扣，折扣额 = floor(放行金额 * 折扣千分比 / 1000)；超过折扣期付全额。
// 账期规则：payAt > releasedAt + PaymentTermSec（严格大于）记逾期标志，
// 付款仍然成功。折扣期与账期均取自发票行所属订单；规范中一张发票各行
// 可能来自同供应商的不同订单，要求账期/折扣条件一致才能给出唯一付款额，
// 故放行金额折扣基于“首个行所属订单”的条款——为避免多订单语义歧义，
// 提交结构校验要求同一张发票各行只能引用同一张采购订单（见
// validateSinglePO）。
//
// 拒绝次序：参数非法 > 时钟回退 > 对象不存在（发票）> 供应商冻结 >
// 状态不符（未放行/被保留）> 已付款（业务拒绝）。
func (s *Service) Pay(at int64, invoiceNumber string) (PaymentResult, *Error) {
	if invoiceNumber == "" {
		return PaymentResult{}, newError(KindInvalidParam, "发票号不能为空")
	}
	if at < 0 {
		return PaymentResult{}, newError(KindInvalidParam, "付款时刻不能为负: %d", at)
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return PaymentResult{}, err
	}
	inv, ok := s.st.invoices[invoiceNumber]
	if !ok {
		return PaymentResult{}, newError(KindNotFound, "发票不存在: %s", invoiceNumber)
	}
	sup := s.st.suppliers[inv.supplierID]
	if sup.frozen {
		return PaymentResult{}, newError(KindSupplierFrozen, "供应商 %s 已冻结", inv.supplierID)
	}
	if inv.status == StatusHeld {
		return PaymentResult{}, newError(KindInvalidState,
			"发票 %s 仍被保留，不能付款", invoiceNumber)
	}
	if inv.status == StatusPaid {
		return PaymentResult{}, newError(KindAlreadyPaid, "发票 %s 已付款", invoiceNumber)
	}

	po := s.st.pos[inv.lines[0].POID]
	terms := po.def
	discount := int64(0)
	if at <= inv.releasedAt+terms.DiscountPeriodSec {
		discount = mulFloorDiv(inv.amount, terms.DiscountPermil)
	}
	paid := inv.amount - discount
	overdue := at > inv.releasedAt+terms.PaymentTermSec

	inv.status = StatusPaid
	inv.paidAt = at
	inv.paidAmount = paid
	inv.discount = discount
	inv.overdue = overdue
	s.st.advance(at)
	s.emit("payment.paid", map[string]any{
		"invoice": inv.number, "at": at, "amount": inv.amount,
		"discount": discount, "paid": paid, "overdue": overdue,
		"discount_deadline": inv.releasedAt + terms.DiscountPeriodSec,
		"term_deadline":     inv.releasedAt + terms.PaymentTermSec,
	})
	return PaymentResult{
		InvoiceNumber: inv.number,
		PaidAt:        at,
		PaidAmount:    paid,
		Discount:      discount,
		Overdue:       overdue,
	}, nil
}
