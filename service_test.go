package matching

import (
	"strings"
	"sync"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService()
}

func mustRegister(t *testing.T, service *Service, supplierID string, at int64) {
	t.Helper()
	if err := service.RegisterSupplier(Supplier{ID: supplierID, Name: supplierID}, at); err != nil {
		t.Fatalf("RegisterSupplier() error = %v", err)
	}
}

func mustOrder(t *testing.T, service *Service, order PurchaseOrder, at int64) {
	t.Helper()
	if err := service.CreatePurchaseOrder(order, at); err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
}

func standardOrder(id, supplierID string) PurchaseOrder {
	return PurchaseOrder{
		ID:                     id,
		SupplierID:             supplierID,
		OverReceiptPermille:    100,
		PriceTolerancePermille: 100,
		PaymentPeriodSeconds:   30,
		DiscountPeriodSeconds:  10,
		DiscountPermille:       20,
		Lines: []PurchaseOrderLine{{
			LineID:         "L1",
			Product:        "P1",
			Quantity:       10,
			UnitPriceCents: 1000,
		}},
	}
}

func mustReceipt(t *testing.T, service *Service, receipt GoodsReceipt) ReceiptResult {
	t.Helper()
	result, err := service.RecordReceipt(receipt)
	if err != nil {
		t.Fatalf("RecordReceipt(%+v) error = %v", receipt, err)
	}
	return result
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if got := GetCode(err); got != want {
		t.Fatalf("error code = %q, want %q (err=%v)", got, want, err)
	}
}

func TestReceiptToleranceFloorAndBoundary(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	order := standardOrder("O1", "S1")
	order.Lines[0].Quantity = 3
	mustOrder(t, service, order, 1)

	result := mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 3, Time: 2})
	if result.ReceivedQuantity != 3 {
		t.Fatalf("received = %d, want 3", result.ReceivedQuantity)
	}
	_, err := service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 3})
	assertCode(t, err, ErrOverReceipt)
}

func TestPriceToleranceFloorAndBoundary(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	order := standardOrder("O1", "S1")
	order.Lines[0].UnitPriceCents = 15
	mustOrder(t, service, order, 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 10, Time: 2})

	approved, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 10, UnitPriceCents: 16}},
		Time:       3,
	})
	if err != nil {
		t.Fatalf("exact tolerance invoice error = %v", err)
	}
	if approved.Status != InvoiceApproved || approved.AmountCents != 160 {
		t.Fatalf("approved result = %+v", approved)
	}

	held, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I2",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 17}},
		Time:       4,
	})
	if err != nil {
		t.Fatalf("one beyond tolerance invoice error = %v", err)
	}
	if held.Status != InvoiceHeld || held.FailureCode != ErrPriceMismatch || held.FailureLine != 0 {
		t.Fatalf("held result = %+v", held)
	}
}

func TestReceiptReversalRespectsApprovedInvoices(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 6, Time: 2})
	_, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 4, UnitPriceCents: 1000}},
		Time:       3,
	})
	if err != nil {
		t.Fatalf("SubmitInvoice() error = %v", err)
	}

	_, err = service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 3, Time: 4, Reverse: true})
	assertCode(t, err, ErrReceiptInvoiceHeld)
	result := mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 2, Time: 5, Reverse: true})
	if result.ReceivedQuantity != 4 {
		t.Fatalf("received = %d, want 4", result.ReceivedQuantity)
	}
	_, err = service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 5, Time: 6, Reverse: true})
	assertCode(t, err, ErrNegativeReceipt)
}

func TestHeldInvoiceApprovesAfterAdditionalReceipt(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 4, Time: 2})
	held, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 5, UnitPriceCents: 1000}},
		Time:       3,
	})
	if err != nil || held.Status != InvoiceHeld {
		t.Fatalf("initial result = %+v, err=%v", held, err)
	}

	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 4})
	approved, err := service.ReevaluateInvoice("I1", 5)
	if err != nil {
		t.Fatalf("ReevaluateInvoice() error = %v", err)
	}
	if approved.Status != InvoiceApproved || approved.ApprovedAt != 5 || approved.AmountCents != 5000 {
		t.Fatalf("approved result = %+v", approved)
	}
}

func TestInvoiceFailurePriorityAndMultipleInvoices(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	order := standardOrder("O1", "S1")
	order.Lines = append(order.Lines, PurchaseOrderLine{LineID: "L2", Product: "P2", Quantity: 10, UnitPriceCents: 100})
	mustOrder(t, service, order, 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 10, Time: 2})

	held, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines: []InvoiceLine{
			{LineID: "MISSING", Quantity: 1, UnitPriceCents: 1},
			{LineID: "L1", Quantity: 100, UnitPriceCents: 1000},
		},
		Time: 3,
	})
	if err != nil || held.FailureCode != ErrOrderLineNotFound || held.FailureLine != 0 {
		t.Fatalf("missing priority result = %+v, err=%v", held, err)
	}

	approved2, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I2",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 6, UnitPriceCents: 1000}},
		Time:       4,
	})
	if err != nil || approved2.Status != InvoiceApproved {
		t.Fatalf("first valid invoice result = %+v, err=%v", approved2, err)
	}

	approved1, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I3",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 4, UnitPriceCents: 1000}},
		Time:       5,
	})
	if err != nil || approved1.Status != InvoiceApproved {
		t.Fatalf("first approved result = %+v, err=%v", approved1, err)
	}
	held, err = service.SubmitInvoice(Invoice{
		InvoiceID:  "I4",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 4, UnitPriceCents: 1000}},
		Time:       6,
	})
	if err != nil || held.Status != InvoiceHeld || held.FailureCode != ErrOverInvoiced {
		t.Fatalf("third invoice should be over-invoiced: %+v, err=%v", held, err)
	}
	held, err = service.SubmitInvoice(Invoice{
		InvoiceID:  "I5",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 1000}},
		Time:       7,
	})
	if err != nil || held.Status != InvoiceHeld || held.FailureCode != ErrOverInvoiced {
		t.Fatalf("capacity exhausted result = %+v, err=%v", held, err)
	}
	_, err = service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 1000}},
		Time:       8,
	})
	if err == nil || GetCode(err) != ErrDuplicateInvoice {
		t.Fatalf("retained invoice number should remain duplicate, got %v", err)
	}
}

func TestRepeatedOrderLineWithinOneInvoiceAccumulates(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 5, Time: 2})

	held, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines: []InvoiceLine{
			{LineID: "L1", Quantity: 3, UnitPriceCents: 1000},
			{LineID: "L1", Quantity: 3, UnitPriceCents: 1000},
		},
		Time: 3,
	})
	if err != nil || held.Status != InvoiceHeld || held.FailureCode != ErrOverInvoiced || held.FailureLine != 1 {
		t.Fatalf("repeated line held result = %+v, err=%v", held, err)
	}

	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 4})
	approved, err := service.ReevaluateInvoice("I1", 5)
	if err != nil {
		t.Fatalf("ReevaluateInvoice() error = %v", err)
	}
	if approved.Status != InvoiceApproved || approved.AmountCents != 6000 {
		t.Fatalf("repeated line approved result = %+v", approved)
	}
}

func TestPaymentDiscountAndDueBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		at       int64
		amount   int64
		discount int64
		overdue  bool
	}{
		{name: "discount start", at: 100, amount: 9800, discount: 200},
		{name: "discount end inclusive", at: 110, amount: 9800, discount: 200},
		{name: "after discount", at: 111, amount: 10000},
		{name: "due end not overdue", at: 130, amount: 10000},
		{name: "strictly overdue", at: 131, amount: 10000, overdue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newTestService(t)
			mustRegister(t, service, "S1", 0)
			mustOrder(t, service, standardOrder("O1", "S1"), 1)
			mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 10, Time: 2})
			approved, err := service.SubmitInvoice(Invoice{
				InvoiceID:  "I1",
				SupplierID: "S1",
				OrderID:    "O1",
				Lines:      []InvoiceLine{{LineID: "L1", Quantity: 10, UnitPriceCents: 1000}},
				Time:       100,
			})
			if err != nil || approved.Status != InvoiceApproved {
				t.Fatalf("SubmitInvoice() = %+v, err=%v", approved, err)
			}
			payment, err := service.PayInvoice(Payment{InvoiceID: "I1", Time: tc.at})
			if err != nil {
				t.Fatalf("PayInvoice() error = %v", err)
			}
			if payment.AmountCents != tc.amount || payment.DiscountCents != tc.discount || payment.Overdue != tc.overdue {
				t.Fatalf("payment = %+v, want amount=%d discount=%d overdue=%v", payment, tc.amount, tc.discount, tc.overdue)
			}
			if err := service.SetSupplierBlocked(SupplierBlockCommand{SupplierID: "S1", Blocked: true, Time: tc.at}); err != nil {
				t.Fatalf("SetSupplierBlocked() error = %v", err)
			}
			_, err = service.PayInvoice(Payment{InvoiceID: "I1", Time: tc.at})
			assertCode(t, err, ErrSupplierBlocked)
		})
	}
}

func TestBlockedSupplierRejectsInvoiceAndPermitsReceipt(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 10, Time: 2})
	_, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 5, UnitPriceCents: 1000}},
		Time:       3,
	})
	if err != nil {
		t.Fatalf("SubmitInvoice() error = %v", err)
	}
	if err := service.SetSupplierBlocked(SupplierBlockCommand{SupplierID: "S1", Blocked: true, Time: 4}); err != nil {
		t.Fatalf("SetSupplierBlocked() error = %v", err)
	}

	receipt := mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 5})
	if receipt.ReceivedQuantity != 11 {
		t.Fatalf("receipt while blocked = %+v", receipt)
	}
	_, err = service.SubmitInvoice(Invoice{
		InvoiceID:  "I2",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 1000}},
		Time:       6,
	})
	assertCode(t, err, ErrSupplierBlocked)
	_, err = service.ReevaluateInvoice("I1", 7)
	assertCode(t, err, ErrSupplierBlocked)
	_, err = service.PayInvoice(Payment{InvoiceID: "I1", Time: 8})
	assertCode(t, err, ErrSupplierBlocked)

	if err := service.SetSupplierBlocked(SupplierBlockCommand{SupplierID: "S1", Blocked: false, Time: 9}); err != nil {
		t.Fatalf("unblock error = %v", err)
	}
	payment, err := service.PayInvoice(Payment{InvoiceID: "I1", Time: 10})
	if err != nil {
		t.Fatalf("payment after unblock error = %v", err)
	}
	if payment.Status != InvoicePaid || payment.AmountCents != 4900 {
		t.Fatalf("payment after unblock = %+v", payment)
	}
}

func TestInvoiceApprovedAtZeroCanPay(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 0)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 10, Time: 0})
	approved, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 10, UnitPriceCents: 1000}},
		Time:       0,
	})
	if err != nil || approved.Status != InvoiceApproved || approved.ApprovedAt != 0 {
		t.Fatalf("zero-time approval = %+v, err=%v", approved, err)
	}
	payment, err := service.PayInvoice(Payment{InvoiceID: "I1", Time: 0})
	if err != nil {
		t.Fatalf("zero-time payment error = %v", err)
	}
	if payment.Status != InvoicePaid || payment.AmountCents != 9800 {
		t.Fatalf("zero-time payment = %+v", payment)
	}
}

func TestRejectionPriorityAndClockRejectionNoStateChange(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 10)
	_, err := service.RecordReceipt(GoodsReceipt{OrderID: "", Time: 9})
	assertCode(t, err, ErrInvalidArgument)
	_, err = service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "MISSING", Quantity: 1, Time: 10})
	assertCode(t, err, ErrOrderLineNotFound)
	_, err = service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 9})
	assertCode(t, err, ErrClockRewind)
	result := mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 10})
	if result.ReceivedQuantity != 1 {
		t.Fatalf("rejected receipt changed state: %+v", result)
	}

	_, err = service.PayInvoice(Payment{InvoiceID: "MISSING", Time: 10})
	assertCode(t, err, ErrNotFound)
	held, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 2, UnitPriceCents: 1000}},
		Time:       11,
	})
	if err != nil || held.Status != InvoiceHeld {
		t.Fatalf("held = %+v, err=%v", held, err)
	}
	_, err = service.PayInvoice(Payment{InvoiceID: "I1", Time: 12})
	assertCode(t, err, ErrInvalidState)
}

func TestConcurrentOperationsAreSerializedAndInvariantHolds(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	mustOrder(t, service, standardOrder("O1", "S1"), 1)

	const workers = 16
	const perWorker = 20
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < perWorker; index++ {
				at := int64(2 + worker*perWorker + index)
				_, _ = service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: at})
				_, _ = service.SubmitInvoice(Invoice{
					InvoiceID:  "concurrent",
					SupplierID: "S1",
					OrderID:    "O1",
					Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 1000}},
					Time:       at,
				})
			}
		}(worker)
	}
	wait.Wait()

	line := service.lineIndex[lineKey{orderID: "O1", lineID: "L1"}]
	if line.invoiced > line.received {
		t.Fatalf("invoiced %d exceeds received %d", line.invoiced, line.received)
	}
	if line.received > line.ordered+floorPermille(line.ordered, 100) {
		t.Fatalf("received %d exceeds tolerance limit", line.received)
	}
}

func TestServiceLogsInputOutputAndDecisionBasis(t *testing.T) {
	capture := &CaptureLogger{}
	service := NewService(capture)
	if err := service.RegisterSupplier(Supplier{ID: "S1"}, 0); err != nil {
		t.Fatalf("RegisterSupplier() error = %v", err)
	}
	if err := service.CreatePurchaseOrder(standardOrder("O1", "S1"), 1); err != nil {
		t.Fatalf("CreatePurchaseOrder() error = %v", err)
	}
	if _, err := service.RecordReceipt(GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 2}); err != nil {
		t.Fatalf("RecordReceipt() error = %v", err)
	}
	if _, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1, UnitPriceCents: 1000}},
		Time:       3,
	}); err != nil {
		t.Fatalf("SubmitInvoice() error = %v", err)
	}
	logText := strings.Join(capture.Lines, "\n")
	for _, want := range []string{"input action=submit_invoice", "status=approved", "input action=receipt", "received=1"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}

func TestInvoiceAmountOverflowIsInvalidArgument(t *testing.T) {
	service := newTestService(t)
	mustRegister(t, service, "S1", 0)
	order := standardOrder("O1", "S1")
	order.Lines[0].Quantity = 1
	order.OverReceiptPermille = 0
	mustOrder(t, service, order, 1)
	mustReceipt(t, service, GoodsReceipt{OrderID: "O1", LineID: "L1", Quantity: 1, Time: 2})

	_, err := service.SubmitInvoice(Invoice{
		InvoiceID:  "I1",
		SupplierID: "S1",
		OrderID:    "O1",
		Lines:      []InvoiceLine{{LineID: "L1", Quantity: 1 << 62, UnitPriceCents: 16}},
		Time:       3,
	})
	assertCode(t, err, ErrInvalidArgument)
}
