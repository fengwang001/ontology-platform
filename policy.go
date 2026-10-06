package matching

import (
	"math"
	"math/big"
)

func applyReceipt(order *orderRecord, line *lineRecord, receipt GoodsReceipt) (ReceiptResult, error) {
	next := line.received + receipt.Quantity
	if receipt.Reverse {
		next = line.received - receipt.Quantity
		if next < 0 {
			return ReceiptResult{}, errorf(ErrNegativeReceipt, "reversal would make received quantity negative")
		}
		if next < line.invoiced {
			return ReceiptResult{}, errorf(ErrReceiptInvoiceHeld, "reversal quantity is already used by approved invoices")
		}
	}
	limit := line.ordered + floorPermille(line.ordered, order.order.OverReceiptPermille)
	if next > limit {
		return ReceiptResult{}, errorf(ErrOverReceipt, "received quantity %d exceeds limit %d", next, limit)
	}
	line.received = next
	return ReceiptResult{ReceivedQuantity: next}, nil
}

func floorPermille(value int64, permille int) int64 {
	if permille <= 1000 {
		return value/1000*int64(permille) + value%1000*int64(permille)/1000
	}
	product := new(big.Int).Mul(big.NewInt(value), big.NewInt(int64(permille)))
	product.Quo(product, big.NewInt(1000))
	if !product.IsInt64() {
		return math.MaxInt64
	}
	return product.Int64()
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func multiplyChecked(left, right int64) (int64, bool) {
	if left != 0 && right > (1<<63-1)/left {
		return 0, true
	}
	return left * right, false
}

func addChecked(left, right int64) (int64, bool) {
	if right > (1<<63-1)-left {
		return 0, true
	}
	return left + right, false
}

func heldResult(index int, code ErrorCode) InvoiceResult {
	return InvoiceResult{
		Status:      InvoiceHeld,
		FailureLine: index,
		FailureCode: code,
		HasFailure:  true,
	}
}

func (s *Service) evaluateInvoiceLocked(invoice Invoice) (InvoiceResult, error) {
	order, exists := s.orders[invoice.OrderID]
	if !exists {
		return InvoiceResult{}, errorf(ErrNotFound, "purchase order not found: %s", invoice.OrderID)
	}
	if order.order.SupplierID != invoice.SupplierID {
		return InvoiceResult{}, errorf(ErrSupplierMismatch, "invoice supplier does not match purchase order supplier")
	}
	staged := make(map[string]int64, len(invoice.Lines))
	var amount int64
	for index, invoiceLine := range invoice.Lines {
		line, lineExists := order.lines[invoiceLine.LineID]
		if !lineExists {
			s.logf("decision invoice=%s line=%d reason=order_line_not_found order_line=%s", invoice.InvoiceID, index, invoiceLine.LineID)
			return heldResult(index, ErrOrderLineNotFound), nil
		}
		allowedPriceDifference := floorPermille(line.unitPriceCents, order.order.PriceTolerancePermille)
		s.logf(
			"decision invoice=%s line=%s invoice_price=%d po_price=%d allowed_diff=%d prior_invoiced=%d staged=%d adding=%d received=%d",
			invoice.InvoiceID, invoiceLine.LineID, invoiceLine.UnitPriceCents, line.unitPriceCents,
			allowedPriceDifference, line.invoiced, staged[line.lineID], invoiceLine.Quantity, line.received,
		)
		if absInt64(invoiceLine.UnitPriceCents-line.unitPriceCents) > allowedPriceDifference {
			s.logf("decision invoice=%s line=%d reason=price_mismatch", invoice.InvoiceID, index)
			return heldResult(index, ErrPriceMismatch), nil
		}
		nextInvoiced := line.invoiced + staged[line.lineID] + invoiceLine.Quantity
		if nextInvoiced > line.received {
			s.logf("decision invoice=%s line=%d reason=over_invoiced next_invoiced=%d received=%d", invoice.InvoiceID, index, nextInvoiced, line.received)
			return heldResult(index, ErrOverInvoiced), nil
		}
		lineAmount := invoiceLine.Quantity * invoiceLine.UnitPriceCents
		var overflow bool
		amount, overflow = addChecked(amount, lineAmount)
		if overflow {
			return InvoiceResult{}, errorf(ErrInvalidArgument, "invoice amount overflows")
		}
		staged[line.lineID] += invoiceLine.Quantity
	}
	s.logf("decision invoice=%s all_lines_passed amount=%d", invoice.InvoiceID, amount)
	return InvoiceResult{Status: InvoiceApproved, AmountCents: amount, ApprovedAt: invoice.Time}, nil
}

func (s *Service) commitInvoice(invoice Invoice, result InvoiceResult, approvedAt int64) {
	record := &invoiceRecord{
		invoice:     invoice,
		status:      InvoiceApproved,
		amountCents: result.AmountCents,
		approvedAt:  approvedAt,
	}
	s.invoices[invoice.InvoiceID] = record
	s.commitInvoiceQuantities(record, invoice)
}

func (s *Service) commitExistingInvoice(record *invoiceRecord, result InvoiceResult, approvedAt int64) {
	record.status = InvoiceApproved
	record.amountCents = result.AmountCents
	record.approvedAt = approvedAt
	record.failureLine = 0
	record.failureCode = ""
	record.failureMessage = ""
	s.commitInvoiceQuantities(record, record.invoice)
}

func (s *Service) commitInvoiceQuantities(record *invoiceRecord, invoice Invoice) {
	record.lineStates = record.lineStates[:0]
	for _, line := range invoice.Lines {
		record.lineStates = append(record.lineStates, invoiceLineState{
			lineID:         line.LineID,
			quantity:       line.Quantity,
			unitPriceCents: line.UnitPriceCents,
		})
		s.lineIndex[lineKey{orderID: invoice.OrderID, lineID: line.LineID}].invoiced += line.Quantity
	}
}

func newInvoiceRecord(invoice Invoice, result InvoiceResult) *invoiceRecord {
	record := &invoiceRecord{
		invoice:        invoice,
		status:         InvoiceHeld,
		failureLine:    result.FailureLine,
		failureCode:    result.FailureCode,
		failureMessage: string(result.FailureCode),
	}
	for _, line := range invoice.Lines {
		record.lineStates = append(record.lineStates, invoiceLineState{
			lineID:         line.LineID,
			quantity:       line.Quantity,
			unitPriceCents: line.UnitPriceCents,
		})
	}
	return record
}

func payInvoice(order *orderRecord, record *invoiceRecord, payment Payment) (PaymentResult, error) {
	if record.status == InvoicePaid {
		return PaymentResult{}, errorf(ErrInvoiceAlreadyPaid, "invoice already paid: %s", record.invoice.InvoiceID)
	}
	amount := record.amountCents
	discount := int64(0)
	if payment.Time <= record.approvedAt+order.order.DiscountPeriodSeconds {
		discount = floorPermille(amount, order.order.DiscountPermille)
		amount -= discount
	}
	overdue := payment.Time > record.approvedAt+order.order.PaymentPeriodSeconds
	record.status = InvoicePaid
	record.paidAt = payment.Time
	record.paymentAmount = amount
	record.discountCents = discount
	record.overdue = overdue
	return PaymentResult{
		Status:        InvoicePaid,
		AmountCents:   amount,
		DiscountCents: discount,
		Overdue:       overdue,
	}, nil
}
