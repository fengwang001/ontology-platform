package matching

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type naiveLine struct {
	id       string
	ordered  int64
	price    int64
	received int64
	invoiced int64
}

type naiveOrder struct {
	supplierID       string
	lines            map[string]*naiveLine
	overReceipt      int
	priceTolerance   int
	paymentPeriod    int64
	discountPeriod   int64
	discountPermille int
}

type naiveInvoiceLine struct {
	lineID   string
	quantity int64
	price    int64
}

type naiveInvoice struct {
	id         string
	supplierID string
	orderID    string
	lines      []naiveInvoiceLine
	status     InvoiceStatus
	submitted  int64
	approvedAt int64
	amount     int64
	paidAt     int64
	paidAmount int64
	discount   int64
	overdue    bool
}

type naiveModel struct {
	suppliers       map[string]bool
	orders          map[string]*naiveOrder
	invoices        map[string]*naiveInvoice
	lastTime        int64
	lastFailureCode ErrorCode
	log             strings.Builder
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		suppliers: make(map[string]bool),
		orders:    make(map[string]*naiveOrder),
		invoices:  make(map[string]*naiveInvoice),
	}
}

func naiveFloorPermille(value int64, permille int) int64 {
	return value * int64(permille) / 1000
}

func naiveAbs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func convertNaiveLines(lines []InvoiceLine) []naiveInvoiceLine {
	converted := make([]naiveInvoiceLine, 0, len(lines))
	for _, line := range lines {
		converted = append(converted, naiveInvoiceLine{
			lineID:   line.LineID,
			quantity: line.Quantity,
			price:    line.UnitPriceCents,
		})
	}
	return converted
}

func (m *naiveModel) writef(format string, args ...any) {
	fmt.Fprintf(&m.log, format+"\n", args...)
}

func (m *naiveModel) supplier(supplierID string, blocked bool, at int64) ErrorCode {
	m.writef("input register supplier id=%s blocked=%v time=%d", supplierID, blocked, at)
	if supplierID == "" || at < 0 {
		m.writef("output reject invalid_argument")
		return ErrInvalidArgument
	}
	if at < m.lastTime {
		m.writef("output reject clock_rewind")
		return ErrClockRewind
	}
	if _, exists := m.suppliers[supplierID]; exists {
		m.writef("output reject invalid_argument duplicate")
		return ErrInvalidArgument
	}
	m.suppliers[supplierID] = blocked
	m.lastTime = at
	m.writef("output accepted")
	return ""
}

func (m *naiveModel) order(order PurchaseOrder, at int64) ErrorCode {
	m.writef("input order id=%s supplier=%s time=%d lines=%d", order.ID, order.SupplierID, at, len(order.Lines))
	if order.ID == "" || order.SupplierID == "" || at < 0 || len(order.Lines) == 0 {
		return ErrInvalidArgument
	}
	if at < m.lastTime {
		return ErrClockRewind
	}
	if _, exists := m.orders[order.ID]; exists {
		return ErrInvalidArgument
	}
	if _, exists := m.suppliers[order.SupplierID]; !exists {
		return ErrNotFound
	}
	record := &naiveOrder{
		supplierID:       order.SupplierID,
		lines:            make(map[string]*naiveLine),
		overReceipt:      order.OverReceiptPermille,
		priceTolerance:   order.PriceTolerancePermille,
		paymentPeriod:    order.PaymentPeriodSeconds,
		discountPeriod:   order.DiscountPeriodSeconds,
		discountPermille: order.DiscountPermille,
	}
	for _, line := range order.Lines {
		if line.LineID == "" || line.Quantity <= 0 || line.UnitPriceCents <= 0 {
			return ErrInvalidArgument
		}
		if _, exists := record.lines[line.LineID]; exists {
			return ErrInvalidArgument
		}
		record.lines[line.LineID] = &naiveLine{id: line.LineID, ordered: line.Quantity, price: line.UnitPriceCents}
	}
	m.orders[order.ID] = record
	m.lastTime = at
	m.writef("output accepted")
	return ""
}

func (m *naiveModel) block(supplierID string, blocked bool, at int64) ErrorCode {
	m.writef("input block supplier=%s blocked=%v time=%d", supplierID, blocked, at)
	if supplierID == "" || at < 0 {
		return ErrInvalidArgument
	}
	if at < m.lastTime {
		return ErrClockRewind
	}
	if _, exists := m.suppliers[supplierID]; !exists {
		return ErrNotFound
	}
	m.suppliers[supplierID] = blocked
	m.lastTime = at
	m.writef("output accepted")
	return ""
}

func (m *naiveModel) receipt(input GoodsReceipt) ErrorCode {
	m.writef("input receipt order=%s line=%s qty=%d reverse=%v time=%d", input.OrderID, input.LineID, input.Quantity, input.Reverse, input.Time)
	if input.OrderID == "" || input.LineID == "" || input.Quantity <= 0 || input.Time < 0 {
		return ErrInvalidArgument
	}
	if input.Time < m.lastTime {
		return ErrClockRewind
	}
	order, exists := m.orders[input.OrderID]
	if !exists {
		return ErrNotFound
	}
	line, exists := order.lines[input.LineID]
	if !exists {
		return ErrOrderLineNotFound
	}
	next := line.received + input.Quantity
	if input.Reverse {
		next = line.received - input.Quantity
		if next < 0 {
			m.writef("output reject negative_receipt next=%d invoiced=%d", next, line.invoiced)
			return ErrNegativeReceipt
		}
		if next < line.invoiced {
			m.writef("output reject receipt_invoice_held next=%d invoiced=%d", next, line.invoiced)
			return ErrReceiptInvoiceHeld
		}
	}
	limit := line.ordered + naiveFloorPermille(line.ordered, order.overReceipt)
	if next > limit {
		m.writef("output reject over_receipt next=%d limit=%d", next, limit)
		return ErrOverReceipt
	}
	line.received = next
	m.lastTime = input.Time
	m.writef("output accepted received=%d invoiced=%d limit=%d", next, line.invoiced, limit)
	return ""
}

func (m *naiveModel) submit(input Invoice) (InvoiceStatus, ErrorCode, int, int64) {
	m.writef("input submit invoice=%s supplier=%s order=%s time=%d lines=%d", input.InvoiceID, input.SupplierID, input.OrderID, input.Time, len(input.Lines))
	if input.InvoiceID == "" || input.SupplierID == "" || input.OrderID == "" || input.Time < 0 || len(input.Lines) == 0 {
		return "", ErrInvalidArgument, -1, 0
	}
	if input.Time < m.lastTime {
		return "", ErrClockRewind, -1, 0
	}
	if _, exists := m.invoices[input.InvoiceID]; exists {
		return "", ErrDuplicateInvoice, -1, 0
	}
	if _, exists := m.suppliers[input.SupplierID]; !exists {
		return "", ErrNotFound, -1, 0
	}
	if m.suppliers[input.SupplierID] {
		return "", ErrSupplierBlocked, -1, 0
	}
	order, exists := m.orders[input.OrderID]
	if !exists {
		return "", ErrNotFound, -1, 0
	}
	if order.supplierID != input.SupplierID {
		return "", ErrSupplierMismatch, -1, 0
	}
	staged := make(map[string]int64)
	var amount int64
	convertedLines := convertNaiveLines(input.Lines)
	for index, inputLine := range convertedLines {
		line, exists := order.lines[inputLine.lineID]
		if !exists {
			m.writef("decision line=%d missing_order_line=%s", index, inputLine.lineID)
			m.writef("output held line=%d order_line_not_found", index)
			m.storeHeld(input, index, ErrOrderLineNotFound)
			return InvoiceHeld, "", index, 0
		}
		allowed := naiveFloorPermille(line.price, order.priceTolerance)
		m.writef("decision line=%d invoice_price=%d po_price=%d allowed_diff=%d prior_invoiced=%d staged=%d adding=%d received=%d", index, inputLine.price, line.price, allowed, line.invoiced, staged[line.id], inputLine.quantity, line.received)
		if naiveAbs(inputLine.price-line.price) > allowed {
			m.writef("output held line=%d price_mismatch invoice=%d po=%d allowed=%d", index, inputLine.price, line.price, allowed)
			m.storeHeld(input, index, ErrPriceMismatch)
			return InvoiceHeld, "", index, 0
		}
		if line.invoiced+staged[line.id]+inputLine.quantity > line.received {
			m.writef("output held line=%d over_invoiced used=%d staged=%d adding=%d received=%d", index, line.invoiced, staged[line.id], inputLine.quantity, line.received)
			m.storeHeld(input, index, ErrOverInvoiced)
			return InvoiceHeld, "", index, 0
		}
		staged[line.id] += inputLine.quantity
		amount += inputLine.quantity * inputLine.price
	}
	record := &naiveInvoice{
		id:         input.InvoiceID,
		supplierID: input.SupplierID,
		orderID:    input.OrderID,
		lines:      convertNaiveLines(input.Lines),
		submitted:  input.Time,
		status:     InvoiceApproved,
		approvedAt: input.Time,
		amount:     amount,
	}
	m.invoices[input.InvoiceID] = record
	for _, inputLine := range input.Lines {
		order.lines[inputLine.LineID].invoiced += inputLine.Quantity
	}
	m.lastTime = input.Time
	m.writef("output approved amount=%d", amount)
	return InvoiceApproved, "", -1, amount
}

func (m *naiveModel) storeHeld(input Invoice, line int, code ErrorCode) {
	m.lastFailureCode = code
	m.invoices[input.InvoiceID] = &naiveInvoice{
		id:         input.InvoiceID,
		supplierID: input.SupplierID,
		orderID:    input.OrderID,
		lines:      convertNaiveLines(input.Lines),
		status:     InvoiceHeld,
		submitted:  input.Time,
	}
	m.writef("held-reason invoice=%s line=%d code=%s", input.InvoiceID, line, code)
	m.lastTime = input.Time
}

func (m *naiveModel) reevaluate(invoiceID string, at int64) (InvoiceStatus, ErrorCode, int, int64) {
	m.writef("input reevaluate invoice=%s time=%d", invoiceID, at)
	if invoiceID == "" || at < 0 {
		return "", ErrInvalidArgument, -1, 0
	}
	if at < m.lastTime {
		return "", ErrClockRewind, -1, 0
	}
	record, exists := m.invoices[invoiceID]
	if !exists {
		return "", ErrNotFound, -1, 0
	}
	if m.suppliers[record.supplierID] {
		return "", ErrSupplierBlocked, -1, 0
	}
	if record.status != InvoiceHeld {
		return "", ErrInvalidState, -1, 0
	}
	order := m.orders[record.orderID]
	staged := make(map[string]int64)
	var amount int64
	for index, inputLine := range record.lines {
		line := order.lines[inputLine.lineID]
		if line == nil {
			m.lastFailureCode = ErrOrderLineNotFound
			m.writef("output remains held line=%d order_line_not_found", index)
			return InvoiceHeld, "", index, 0
		}
		allowed := naiveFloorPermille(line.price, order.priceTolerance)
		if naiveAbs(inputLine.price-line.price) > allowed {
			m.lastFailureCode = ErrPriceMismatch
			m.writef("output remains held line=%d price_mismatch", index)
			return InvoiceHeld, "", index, 0
		}
		if line.invoiced+staged[line.id]+inputLine.quantity > line.received {
			m.lastFailureCode = ErrOverInvoiced
			m.writef("output remains held line=%d over_invoiced", index)
			return InvoiceHeld, "", index, 0
		}
		staged[line.id] += inputLine.quantity
		amount += inputLine.quantity * inputLine.price
	}
	for _, inputLine := range record.lines {
		order.lines[inputLine.lineID].invoiced += inputLine.quantity
	}
	record.approvedAt = at
	record.amount = amount
	record.status = InvoiceApproved
	m.lastFailureCode = ""
	m.lastTime = at
	m.writef("output approved amount=%d approved_at=%d", amount, at)
	return InvoiceApproved, "", -1, amount
}

func (m *naiveModel) pay(input Payment) (InvoiceStatus, ErrorCode, int64, int64, bool) {
	m.writef("input pay invoice=%s time=%d", input.InvoiceID, input.Time)
	if input.InvoiceID == "" || input.Time < 0 {
		return "", ErrInvalidArgument, 0, 0, false
	}
	if input.Time < m.lastTime {
		return "", ErrClockRewind, 0, 0, false
	}
	record, exists := m.invoices[input.InvoiceID]
	if !exists {
		return "", ErrNotFound, 0, 0, false
	}
	if m.suppliers[record.supplierID] {
		return "", ErrSupplierBlocked, 0, 0, false
	}
	if record.status == InvoicePaid {
		return "", ErrInvoiceAlreadyPaid, 0, 0, false
	}
	if record.status != InvoiceApproved {
		return "", ErrInvalidState, 0, 0, false
	}
	order := m.orders[record.orderID]
	amount := record.amount
	discount := int64(0)
	if input.Time <= record.approvedAt+order.discountPeriod {
		discount = naiveFloorPermille(amount, order.discountPermille)
		amount -= discount
	}
	overdue := input.Time > record.approvedAt+order.paymentPeriod
	record.paidAt = input.Time
	record.status = InvoicePaid
	record.paidAmount = amount
	record.discount = discount
	record.overdue = overdue
	m.lastTime = input.Time
	m.writef("output paid amount=%d discount=%d overdue=%v", amount, discount, overdue)
	return InvoicePaid, "", amount, discount, overdue
}

type operationResult struct {
	kind   string
	code   ErrorCode
	status InvoiceStatus
	amount int64
	line   int
}

type randomOperation struct {
	name string
	run  func(*testing.T, *Service, *naiveModel)
	log  string
}

func TestRandomOperationsMatchNaiveModelSkeleton(t *testing.T) {
	model := newNaiveModel()
	if len(model.suppliers) != 0 || model.log.Len() != 0 {
		t.Fatal("naive model did not initialize")
	}
	_ = rand.New(rand.NewSource(1))
	_ = fmt.Sprint
}

func compareServiceError(t *testing.T, action string, err error, want ErrorCode, model *naiveModel) {
	t.Helper()
	if got := GetCode(err); got != want {
		t.Fatalf("%s service code=%q want=%q\ndecision log:\n%s", action, got, want, model.log.String())
	}
}

func compareInvoiceResult(t *testing.T, action string, got InvoiceResult, wantStatus InvoiceStatus, wantCode ErrorCode, wantLine int, wantAmount int64, err error, model *naiveModel) {
	t.Helper()
	if GetCode(err) != wantCode {
		t.Fatalf("%s service error code=%q want=%q err=%v\ndecision log:\n%s", action, GetCode(err), wantCode, err, model.log.String())
	}
	if wantCode != "" {
		return
	}
	if got.Status != wantStatus || got.AmountCents != wantAmount || (wantStatus == InvoiceHeld && got.FailureLine != wantLine) {
		t.Fatalf("%s result=%+v want status=%s amount=%d line=%d\ndecision log:\n%s", action, got, wantStatus, wantAmount, wantLine, model.log.String())
	}
	if wantStatus == InvoiceHeld && got.FailureCode != model.lastFailureCode {
		t.Fatalf("%s failure code=%q want=%q\ndecision log:\n%s", action, got.FailureCode, model.lastFailureCode, model.log.String())
	}
}

func comparePaymentResult(t *testing.T, got PaymentResult, status InvoiceStatus, code ErrorCode, amount, discount int64, overdue bool, err error, model *naiveModel) {
	t.Helper()
	if GetCode(err) != code {
		t.Fatalf("payment service error code=%q want=%q err=%v\ndecision log:\n%s", GetCode(err), code, err, model.log.String())
	}
	if code != "" {
		return
	}
	if got.Status != status || got.AmountCents != amount || got.DiscountCents != discount || got.Overdue != overdue {
		t.Fatalf("payment result=%+v want status=%s amount=%d discount=%d overdue=%v\ndecision log:\n%s", got, status, amount, discount, overdue, model.log.String())
	}
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping randomized model comparison in short mode")
	}
	rng := rand.New(rand.NewSource(20261006))
	for iteration := 0; iteration < 200; iteration++ {
		service := NewService()
		model := newNaiveModel()
		seedRandomWorld(t, service, model, rng.Int63())
		held := make([]string, 0)
		var invoiceCounter int
		var clock int64

		for step := 0; step < 120; step++ {
			clock++
			supplierID := "S1"
			if rng.Intn(2) == 0 {
				supplierID = "S2"
			}
			orderID := "O-" + supplierID
			lineID := "L1"
			if rng.Intn(2) == 0 {
				lineID = "L2"
			}

			switch rng.Intn(10) {
			case 0, 1, 2:
				quantity := int64(1 + rng.Intn(8))
				reverse := rng.Intn(3) == 0
				input := GoodsReceipt{OrderID: orderID, LineID: lineID, Quantity: quantity, Time: clock, Reverse: reverse}
				_, serviceErr := service.RecordReceipt(input)
				modelCode := model.receipt(input)
				t.Log(model.log.String())
				model.log.Reset()
				compareServiceError(t, "receipt", serviceErr, modelCode, model)
			case 3, 4, 5:
				invoiceCounter++
				if rng.Intn(12) == 0 && supplierID == "S2" {
					supplierID = "S1"
				}
				lines := []InvoiceLine{{
					LineID:         lineID,
					Quantity:       int64(1 + rng.Intn(6)),
					UnitPriceCents: int64(8 + rng.Intn(8)),
				}}
				if rng.Intn(3) == 0 {
					other := "L1"
					if lineID == "L1" {
						other = "L2"
					}
					lines = append(lines, InvoiceLine{
						LineID:         other,
						Quantity:       int64(1 + rng.Intn(5)),
						UnitPriceCents: int64(8 + rng.Intn(8)),
					})
				}
				if rng.Intn(20) == 0 {
					lines[0].LineID = "MISSING"
				}
				input := Invoice{
					InvoiceID:  fmt.Sprintf("I-%d-%d", iteration, invoiceCounter),
					SupplierID: supplierID,
					OrderID:    orderID,
					Lines:      lines,
					Time:       clock,
				}
				result, serviceErr := service.SubmitInvoice(input)
				status, modelCode, failureLine, amount := model.submit(input)
				t.Log(model.log.String())
				model.log.Reset()
				compareInvoiceResult(t, "submit", result, status, modelCode, failureLine, amount, serviceErr, model)
				if serviceErr == nil && result.Status == InvoiceHeld {
					held = append(held, input.InvoiceID)
				}
			case 6:
				if len(held) > 0 {
					index := rng.Intn(len(held))
					invoiceID := held[index]
					result, serviceErr := service.ReevaluateInvoice(invoiceID, clock)
					status, modelCode, failureLine, amount := model.reevaluate(invoiceID, clock)
					t.Log(model.log.String())
					model.log.Reset()
					compareInvoiceResult(t, "reevaluate", result, status, modelCode, failureLine, amount, serviceErr, model)
					if serviceErr == nil && result.Status == InvoiceApproved {
						held = append(held[:index], held[index+1:]...)
					}
				}
			case 7:
				targetSupplier := "S1"
				if rng.Intn(2) == 0 {
					targetSupplier = "S2"
				}
				blocked := rng.Intn(2) == 0
				command := SupplierBlockCommand{SupplierID: targetSupplier, Blocked: blocked, Time: clock}
				serviceErr := service.SetSupplierBlocked(command)
				modelCode := model.block(targetSupplier, blocked, clock)
				t.Log(model.log.String())
				model.log.Reset()
				compareServiceError(t, "block", serviceErr, modelCode, model)
			case 8, 9:
				if invoiceCounter == 0 {
					continue
				}
				invoiceID := fmt.Sprintf("I-%d-%d", iteration, 1+rng.Intn(invoiceCounter))
				input := Payment{InvoiceID: invoiceID, Time: clock}
				result, serviceErr := service.PayInvoice(input)
				status, modelCode, amount, discount, overdue := model.pay(input)
				t.Log(model.log.String())
				model.log.Reset()
				comparePaymentResult(t, result, status, modelCode, amount, discount, overdue, serviceErr, model)
			}
		}
	}
}

func seedRandomWorld(t *testing.T, service *Service, model *naiveModel, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	for _, supplierID := range []string{"S1", "S2"} {
		if err := service.RegisterSupplier(Supplier{ID: supplierID, Name: supplierID}, 0); err != nil {
			t.Fatalf("seed supplier: %v", err)
		}
		if code := model.supplier(supplierID, false, 0); code != "" {
			t.Fatalf("naive seed supplier: %s", code)
		}
	}
	for _, supplierID := range []string{"S1", "S2"} {
		order := PurchaseOrder{
			ID:                     "O-" + supplierID,
			SupplierID:             supplierID,
			OverReceiptPermille:    150,
			PriceTolerancePermille: 250,
			PaymentPeriodSeconds:   40,
			DiscountPeriodSeconds:  10,
			DiscountPermille:       50,
			Lines: []PurchaseOrderLine{
				{LineID: "L1", Product: "P1", Quantity: int64(20 + rng.Intn(10)), UnitPriceCents: 10},
				{LineID: "L2", Product: "P2", Quantity: int64(20 + rng.Intn(10)), UnitPriceCents: 20},
			},
		}
		if err := service.CreatePurchaseOrder(order, 1); err != nil {
			t.Fatalf("seed order: %v", err)
		}
		if code := model.order(order, 1); code != "" {
			t.Fatalf("naive seed order: %s", code)
		}
	}
}
