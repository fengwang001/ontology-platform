package matching

type Supplier struct {
	ID      string
	Name    string
	Blocked bool
}

type PurchaseOrderLine struct {
	LineID         string
	Product        string
	Quantity       int64
	UnitPriceCents int64
}

type PurchaseOrder struct {
	ID                     string
	SupplierID             string
	Lines                  []PurchaseOrderLine
	OverReceiptPermille    int
	PriceTolerancePermille int
	PaymentPeriodSeconds   int64
	DiscountPeriodSeconds  int64
	DiscountPermille       int
}

type GoodsReceipt struct {
	OrderID  string
	LineID   string
	Quantity int64
	Time     int64
	Reverse  bool
}

type ReceiptResult struct {
	ReceivedQuantity int64
}

type InvoiceLine struct {
	LineID         string
	Quantity       int64
	UnitPriceCents int64
}

type Invoice struct {
	InvoiceID  string
	SupplierID string
	OrderID    string
	Lines      []InvoiceLine
	Time       int64
}

type InvoiceStatus string

const (
	InvoiceHeld     InvoiceStatus = "held"
	InvoiceApproved InvoiceStatus = "approved"
	InvoicePaid     InvoiceStatus = "paid"
)

type InvoiceResult struct {
	Status      InvoiceStatus
	AmountCents int64
	FailureLine int
	FailureCode ErrorCode
	ApprovedAt  int64
	HasFailure  bool
}

type Payment struct {
	InvoiceID string
	Time      int64
}

type PaymentResult struct {
	Status        InvoiceStatus
	AmountCents   int64
	DiscountCents int64
	Overdue       bool
}

type SupplierBlockCommand struct {
	SupplierID string
	Blocked    bool
	Time       int64
}
