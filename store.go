package matching

import (
	"sync"
)

type lineKey struct {
	orderID string
	lineID  string
}

type supplierRecord struct {
	supplier Supplier
}

type lineRecord struct {
	orderID        string
	lineID         string
	product        string
	ordered        int64
	unitPriceCents int64
	received       int64
	invoiced       int64
}

type orderRecord struct {
	order PurchaseOrder
	lines map[string]*lineRecord
}

type invoiceRecord struct {
	invoice        Invoice
	status         InvoiceStatus
	lineStates     []invoiceLineState
	amountCents    int64
	approvedAt     int64
	failureLine    int
	failureCode    ErrorCode
	failureMessage string
	paidAt         int64
	paymentAmount  int64
	discountCents  int64
	overdue        bool
}

type invoiceLineState struct {
	lineID         string
	quantity       int64
	unitPriceCents int64
}

type Service struct {
	mu        sync.RWMutex
	lastTime  int64
	suppliers map[string]*supplierRecord
	orders    map[string]*orderRecord
	invoices  map[string]*invoiceRecord
	lineIndex map[lineKey]*lineRecord
	logger    Logger
}

func NewService(logger ...Logger) *Service {
	var log Logger
	if len(logger) > 0 {
		log = logger[0]
	}
	return &Service{
		suppliers: make(map[string]*supplierRecord),
		orders:    make(map[string]*orderRecord),
		invoices:  make(map[string]*invoiceRecord),
		lineIndex: make(map[lineKey]*lineRecord),
		logger:    log,
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func (s *Service) logError(action string, err error) {
	if err != nil {
		s.logf("output action=%s rejected code=%s reason=%q", action, GetCode(err), err.Error())
	}
}

func (s *Service) RegisterSupplier(supplier Supplier, at int64) error {
	s.logf("input action=register_supplier supplier=%s time=%d", supplier.ID, at)
	if err := validateSupplier(supplier, at); err != nil {
		s.logError("register_supplier", err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		s.logError("register_supplier", err)
		return err
	}
	if _, exists := s.suppliers[supplier.ID]; exists {
		err := errorf(ErrInvalidArgument, "supplier already exists: %s", supplier.ID)
		s.logError("register_supplier", err)
		return err
	}
	s.suppliers[supplier.ID] = &supplierRecord{supplier: supplier}
	s.lastTime = at
	s.logf("output action=register_supplier accepted supplier=%s time=%d", supplier.ID, at)
	return nil
}

func (s *Service) CreatePurchaseOrder(order PurchaseOrder, at int64) error {
	s.logf("input action=create_order order=%s supplier=%s lines=%d time=%d", order.ID, order.SupplierID, len(order.Lines), at)
	if err := validatePurchaseOrder(order, at); err != nil {
		s.logError("create_order", err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		s.logError("create_order", err)
		return err
	}
	if _, exists := s.orders[order.ID]; exists {
		err := errorf(ErrInvalidArgument, "purchase order already exists: %s", order.ID)
		s.logError("create_order", err)
		return err
	}
	if _, exists := s.suppliers[order.SupplierID]; !exists {
		err := errorf(ErrNotFound, "supplier not found: %s", order.SupplierID)
		s.logError("create_order", err)
		return err
	}
	record := &orderRecord{order: order, lines: make(map[string]*lineRecord)}
	for _, line := range order.Lines {
		lineRecord := &lineRecord{
			orderID:        order.ID,
			lineID:         line.LineID,
			product:        line.Product,
			ordered:        line.Quantity,
			unitPriceCents: line.UnitPriceCents,
		}
		record.lines[line.LineID] = lineRecord
		s.lineIndex[lineKey{orderID: order.ID, lineID: line.LineID}] = lineRecord
	}
	s.orders[order.ID] = record
	s.lastTime = at
	s.logf("output action=create_order accepted order=%s lines=%d", order.ID, len(order.Lines))
	return nil
}

func (s *Service) SetSupplierBlocked(command SupplierBlockCommand) error {
	s.logf("input action=set_supplier_blocked supplier=%s blocked=%v time=%d", command.SupplierID, command.Blocked, command.Time)
	if command.SupplierID == "" || command.Time < 0 {
		err := errorf(ErrInvalidArgument, "invalid supplier block command")
		s.logError("set_supplier_blocked", err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(command.Time); err != nil {
		s.logError("set_supplier_blocked", err)
		return err
	}
	supplier, exists := s.suppliers[command.SupplierID]
	if !exists {
		err := errorf(ErrNotFound, "supplier not found: %s", command.SupplierID)
		s.logError("set_supplier_blocked", err)
		return err
	}
	supplier.supplier.Blocked = command.Blocked
	s.lastTime = command.Time
	s.logf("output action=set_supplier_blocked accepted supplier=%s blocked=%v", command.SupplierID, command.Blocked)
	return nil
}

func (s *Service) RecordReceipt(receipt GoodsReceipt) (ReceiptResult, error) {
	s.logf("input action=receipt order=%s line=%s qty=%d reverse=%v time=%d", receipt.OrderID, receipt.LineID, receipt.Quantity, receipt.Reverse, receipt.Time)
	if err := validateReceipt(receipt); err != nil {
		s.logError("receipt", err)
		return ReceiptResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(receipt.Time); err != nil {
		s.logError("receipt", err)
		return ReceiptResult{}, err
	}
	line, err := s.findLineIndexed(receipt.OrderID, receipt.LineID)
	if err != nil {
		s.logError("receipt", err)
		return ReceiptResult{}, err
	}
	order := s.orders[receipt.OrderID]
	result, err := applyReceipt(order, line, receipt)
	if err != nil {
		s.logError("receipt", err)
		return ReceiptResult{}, err
	}
	s.lastTime = receipt.Time
	s.logf("output action=receipt accepted received=%d invoiced=%d ordered=%d", result.ReceivedQuantity, line.invoiced, line.ordered)
	return result, nil
}

func (s *Service) SubmitInvoice(invoice Invoice) (InvoiceResult, error) {
	s.logf("input action=submit_invoice invoice=%s supplier=%s order=%s lines=%d time=%d", invoice.InvoiceID, invoice.SupplierID, invoice.OrderID, len(invoice.Lines), invoice.Time)
	if err := validateInvoice(invoice); err != nil {
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(invoice.Time); err != nil {
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	if _, exists := s.invoices[invoice.InvoiceID]; exists {
		err := errorf(ErrDuplicateInvoice, "invoice already exists: %s", invoice.InvoiceID)
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	supplier, exists := s.suppliers[invoice.SupplierID]
	if !exists {
		err := errorf(ErrNotFound, "supplier not found: %s", invoice.SupplierID)
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	if supplier.supplier.Blocked {
		err := errorf(ErrSupplierBlocked, "supplier is blocked: %s", invoice.SupplierID)
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	result, err := s.evaluateInvoiceLocked(invoice)
	if err != nil {
		s.logError("submit_invoice", err)
		return InvoiceResult{}, err
	}
	if result.Status == InvoiceApproved {
		s.commitInvoice(invoice, result, invoice.Time)
	} else {
		s.invoices[invoice.InvoiceID] = newInvoiceRecord(invoice, result)
	}
	s.lastTime = invoice.Time
	s.logf("output action=submit_invoice invoice=%s status=%s amount=%d failure_line=%d failure_code=%s", invoice.InvoiceID, result.Status, result.AmountCents, result.FailureLine, result.FailureCode)
	return result, nil
}

func (s *Service) ReevaluateInvoice(invoiceID string, at int64) (InvoiceResult, error) {
	s.logf("input action=reevaluate_invoice invoice=%s time=%d", invoiceID, at)
	if invoiceID == "" || at < 0 {
		err := errorf(ErrInvalidArgument, "invalid invoice reevaluation")
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	record, exists := s.invoices[invoiceID]
	if !exists {
		err := errorf(ErrNotFound, "invoice not found: %s", invoiceID)
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	supplier := s.suppliers[record.invoice.SupplierID]
	if supplier == nil || supplier.supplier.Blocked {
		err := errorf(ErrSupplierBlocked, "supplier is blocked: %s", record.invoice.SupplierID)
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	if record.status != InvoiceHeld {
		err := errorf(ErrInvalidState, "invoice is not held: %s", invoiceID)
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	invoice := record.invoice
	invoice.Time = at
	result, err := s.evaluateInvoiceLocked(invoice)
	if err != nil {
		s.logError("reevaluate_invoice", err)
		return InvoiceResult{}, err
	}
	if result.Status == InvoiceApproved {
		s.commitExistingInvoice(record, result, at)
		s.lastTime = at
	}
	s.logf("output action=reevaluate_invoice invoice=%s status=%s amount=%d failure_line=%d failure_code=%s", invoiceID, result.Status, result.AmountCents, result.FailureLine, result.FailureCode)
	return result, nil
}

func (s *Service) PayInvoice(payment Payment) (PaymentResult, error) {
	s.logf("input action=pay_invoice invoice=%s time=%d", payment.InvoiceID, payment.Time)
	if payment.InvoiceID == "" || payment.Time < 0 {
		err := errorf(ErrInvalidArgument, "invalid payment")
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(payment.Time); err != nil {
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	record, exists := s.invoices[payment.InvoiceID]
	if !exists {
		err := errorf(ErrNotFound, "invoice not found: %s", payment.InvoiceID)
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	supplier := s.suppliers[record.invoice.SupplierID]
	if supplier == nil || supplier.supplier.Blocked {
		err := errorf(ErrSupplierBlocked, "supplier is blocked: %s", record.invoice.SupplierID)
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	if record.status == InvoicePaid {
		err := errorf(ErrInvoiceAlreadyPaid, "invoice already paid: %s", payment.InvoiceID)
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	if record.status != InvoiceApproved {
		err := errorf(ErrInvalidState, "invoice is not approved: %s", payment.InvoiceID)
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	order, exists := s.orders[record.invoice.OrderID]
	if !exists {
		err := errorf(ErrNotFound, "purchase order not found: %s", record.invoice.OrderID)
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	result, err := payInvoice(order, record, payment)
	if err != nil {
		s.logError("pay_invoice", err)
		return PaymentResult{}, err
	}
	s.lastTime = payment.Time
	s.logf("output action=pay_invoice invoice=%s amount=%d discount=%d overdue=%v", payment.InvoiceID, result.AmountCents, result.DiscountCents, result.Overdue)
	return result, nil
}

func (s *Service) checkClock(at int64) error {
	if at < s.lastTime {
		return errorf(ErrClockRewind, "time %d is before last accepted time %d", at, s.lastTime)
	}
	return nil
}

func (s *Service) findLine(orderID, lineID string) (*lineRecord, error) {
	order, exists := s.orders[orderID]
	if !exists {
		return nil, errorf(ErrNotFound, "purchase order not found: %s", orderID)
	}
	line, exists := order.lines[lineID]
	if !exists {
		return nil, errorf(ErrOrderLineNotFound, "purchase order line not found: %s/%s", orderID, lineID)
	}
	return line, nil
}

func (s *Service) findLineIndexed(orderID, lineID string) (*lineRecord, error) {
	line, exists := s.lineIndex[lineKey{orderID: orderID, lineID: lineID}]
	if exists {
		return line, nil
	}
	if _, orderExists := s.orders[orderID]; !orderExists {
		return nil, errorf(ErrNotFound, "purchase order not found: %s", orderID)
	}
	return nil, lineErrorf(ErrOrderLineNotFound, 0, "purchase order line not found: %s/%s", orderID, lineID)
}
