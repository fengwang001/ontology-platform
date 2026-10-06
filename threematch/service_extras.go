package threematch

// Clock 返回系统单调时钟（上一次被接受操作的时刻）。初始为 0。
func (s *Service) Clock() int64 {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	return s.st.clock
}

// LineQuantities 返回订单行的当前数量三元组：
//
//	ordered   订购量
//	received  累计收货量（冲销后净值）
//	invoiced  已放行发票累计占用数量
func (s *Service) LineQuantities(poID string, lineIndex int64) (ordered, received, invoiced int64, err *Error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	_, line, e := s.resolveLine(poID, lineIndex)
	if e != nil {
		return 0, 0, 0, newError(KindNotFound, "%s", e.Msg)
	}
	return line.def.Quantity, line.received, line.invoicedQty, nil
}

// explainLine 返回订单行在给定发票行上的判定依据，供日志与测试使用。
// 该函数只读且无副作用，复杂度 O(1)。
type LineExplanation struct {
	POID             string
	LineIndex        int64
	OrderedQty       int64
	ReceivedQty      int64
	InvoicedQty      int64
	AvailableQty     int64 // received - invoiced
	OverReceiptAllow int64
	ReceiptCap       int64
	POUnitPrice      int64
	InvoiceUnitPrice int64
	PriceAllowance   int64
	PriceDiff        int64
	PriceOK          bool
	QuantityOK       bool
}

// ExplainLine 解释某张被保留/已放行发票中一行的当前判定依据。
// 查询不推进时钟。
func (s *Service) ExplainLine(invoiceNumber string, lineSlot int) (LineExplanation, *Error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	inv, ok := s.st.invoices[invoiceNumber]
	if !ok {
		return LineExplanation{}, newError(KindNotFound, "发票不存在: %s", invoiceNumber)
	}
	if lineSlot < 0 || lineSlot >= len(inv.lines) {
		return LineExplanation{}, newError(KindInvalidParam,
			"发票 %s 行槽位越界: %d", invoiceNumber, lineSlot)
	}
	ln := inv.lines[lineSlot]
	po, line, e := s.resolveLine(ln.POID, ln.LineIndex)
	if e != nil {
		return LineExplanation{}, e
	}
	priceAllow := mulFloorDiv(line.def.UnitPrice, po.def.PriceTolerancePermil)
	diff := ln.UnitPrice - line.def.UnitPrice
	if diff < 0 {
		diff = -diff
	}
	return LineExplanation{
		POID:             ln.POID,
		LineIndex:        ln.LineIndex,
		OrderedQty:       line.def.Quantity,
		ReceivedQty:      line.received,
		InvoicedQty:      line.invoicedQty,
		AvailableQty:     line.received - line.invoicedQty,
		OverReceiptAllow: line.overReceiptAllowanceSafe(po.def.OverReceiptPermil),
		ReceiptCap:       line.maxReceivableSafe(po.def.OverReceiptPermil),
		POUnitPrice:      line.def.UnitPrice,
		InvoiceUnitPrice: ln.UnitPrice,
		PriceAllowance:   priceAllow,
		PriceDiff:        diff,
		PriceOK:          diff <= priceAllow,
		QuantityOK:       ln.Quantity <= line.received-line.invoicedQty,
	}, nil
}
