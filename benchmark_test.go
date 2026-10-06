package matching

import "testing"

func buildScaledService(b *testing.B, orderCount, receiptsPerLine int) (*Service, PurchaseOrder) {
	b.Helper()
	service := NewService()
	if err := service.RegisterSupplier(Supplier{ID: "S-TARGET"}, 0); err != nil {
		b.Fatal(err)
	}
	if orderCount > 1 {
		if err := service.RegisterSupplier(Supplier{ID: "S-OTHER"}, 0); err != nil {
			b.Fatal(err)
		}
	}

	var target PurchaseOrder
	for index := 0; index < orderCount; index++ {
		supplierID := "S-TARGET"
		if index > 0 {
			supplierID = "S-OTHER"
		}
		order := PurchaseOrder{
			ID:                     orderBenchmarkID(index),
			SupplierID:             supplierID,
			OverReceiptPermille:    100,
			PriceTolerancePermille: 100,
			PaymentPeriodSeconds:   60,
			DiscountPeriodSeconds:  10,
			DiscountPermille:       20,
			Lines: []PurchaseOrderLine{{
				LineID:         "L1",
				Product:        "P1",
				Quantity:       int64(receiptsPerLine + 10),
				UnitPriceCents: 1000,
			}},
		}
		if err := service.CreatePurchaseOrder(order, int64(index+1)); err != nil {
			b.Fatal(err)
		}
		if index == 0 {
			target = order
		}
	}
	var receiptClock int64 = int64(orderCount)
	for index := 0; index < orderCount; index++ {
		for receiptIndex := 0; receiptIndex < receiptsPerLine; receiptIndex++ {
			receiptClock++
			if _, err := service.RecordReceipt(GoodsReceipt{
				OrderID:  orderBenchmarkID(index),
				LineID:   "L1",
				Quantity: 1,
				Time:     receiptClock,
			}); err != nil {
				b.Fatal(err)
			}
		}
	}
	return service, target
}

func orderBenchmarkID(index int) string {
	if index == 0 {
		return "O-TARGET"
	}
	return "O-OTHER-" + string(rune('a'+index%26)) + string(rune('a'+(index/26)%26)) + string(rune('a'+(index/676)%26))
}

func BenchmarkSubmitOneLineInvoice(b *testing.B) {
	cases := []struct {
		name       string
		orderCount int
		receipts   int
	}{
		{name: "10_orders_10_receipts", orderCount: 10, receipts: 10},
		{name: "1000_orders_100_receipts", orderCount: 1000, receipts: 100},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			service, _ := buildScaledService(b, tc.orderCount, tc.receipts)
			baseTime := int64(tc.orderCount*tc.receipts + tc.orderCount + 1)
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				invoice := Invoice{
					InvoiceID:  "I-BENCH-" + benchmarkSuffix(iteration),
					SupplierID: "S-TARGET",
					OrderID:    "O-TARGET",
					Lines:      []InvoiceLine{{LineID: "L1", Quantity: int64(tc.receipts + 1), UnitPriceCents: 1000}},
					Time:       baseTime + int64(iteration),
				}
				result, err := service.SubmitInvoice(invoice)
				if err != nil || result.Status != InvoiceHeld || result.FailureCode != ErrOverInvoiced {
					b.Fatalf("SubmitInvoice() = %+v, %v", result, err)
				}
			}
		})
	}
}

func benchmarkSuffix(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('a' + value%26)}, digits...)
		value /= 26
	}
	return string(digits)
}
