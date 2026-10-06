package threematch

// POLine 是采购订单行的静态定义。金额单位均为“分”。
type POLine struct {
	Item      string // 商品标识
	Quantity  int64  // 订购量，必须为正
	UnitPrice int64  // 单价（分），必须为正
}

// PurchaseOrder 是一张采购订单的静态定义。
//
// 各容差以千分比表示：OverReceiptPermil=50 表示允许超收 5%。
// PaymentTermSec 为账期秒数；DiscountPeriodSec 为折扣期秒数；
// DiscountPermil 为折扣千分比。
type PurchaseOrder struct {
	ID                   string
	SupplierID           string
	Lines                []POLine
	OverReceiptPermil    int64
	PriceTolerancePermil int64
	PaymentTermSec       int64
	DiscountPeriodSec    int64
	DiscountPermil       int64
}

// poLineState 是订单行的运行态：维护累计收货量与已放行发票占用数量。
type poLineState struct {
	def         POLine
	received    int64 // 累计收货量（含冲销后的净值，永不为负）
	invoicedQty int64 // 已放行发票累计占用数量
}

// poState 是采购订单的运行态。
type poState struct {
	def   PurchaseOrder
	lines []*poLineState
}

// InvoiceLine 是提交发票时的一行。POID 与 LineIndex 共同定位订单行，
// LineIndex 为采购订单行下标（从 0 起）。一张发票的各行可引用同一供应商
// 的不同订单，但不得引用其他供应商的订单。
type InvoiceLine struct {
	POID      string
	LineIndex int64
	Quantity  int64 // 数量，必须为正
	UnitPrice int64 // 发票单价（分），必须为正
}

// InvoiceStatus 为发票生命周期状态。
type InvoiceStatus int

const (
	// StatusHeld 表示发票被保留（至少一行未通过），不占用任何匹配额度。
	StatusHeld InvoiceStatus = iota + 1
	// StatusReleased 表示发票已放行，可付款。
	StatusReleased
	// StatusPaid 表示发票已付款。
	StatusPaid
)

func (s InvoiceStatus) String() string {
	switch s {
	case StatusHeld:
		return "held"
	case StatusReleased:
		return "released"
	case StatusPaid:
		return "paid"
	default:
		return "unknown"
	}
}

// invoiceState 是发票运行态。
type invoiceState struct {
	number     string
	supplierID string
	lines      []InvoiceLine
	status     InvoiceStatus
	amount     int64 // 放行金额（分）
	releasedAt int64 // 放行时刻
	paidAt     int64 // 付款时刻
	paidAmount int64 // 实付金额
	discount   int64 // 折扣额
	overdue    bool  // 逾期标志
	failLine   int   // 最近一次判定的首个失败行下标
	failReason ErrorKind
}

// InvoiceSnapshot 是发票对外只读快照。
type InvoiceSnapshot struct {
	Number     string
	SupplierID string
	Lines      []InvoiceLine
	Status     InvoiceStatus
	Amount     int64
	ReleasedAt int64
	PaidAt     int64
	PaidAmount int64
	Discount   int64
	Overdue    bool
	FailLine   int
	FailReason ErrorKind
}

func (inv *invoiceState) snapshot() InvoiceSnapshot {
	lines := make([]InvoiceLine, len(inv.lines))
	copy(lines, inv.lines)
	return InvoiceSnapshot{
		Number:     inv.number,
		SupplierID: inv.supplierID,
		Lines:      lines,
		Status:     inv.status,
		Amount:     inv.amount,
		ReleasedAt: inv.releasedAt,
		PaidAt:     inv.paidAt,
		PaidAmount: inv.paidAmount,
		Discount:   inv.discount,
		Overdue:    inv.overdue,
		FailLine:   inv.failLine,
		FailReason: inv.failReason,
	}
}

// PaymentResult 是付款结果。
type PaymentResult struct {
	InvoiceNumber string
	PaidAt        int64
	PaidAmount    int64 // 实付金额（分）
	Discount      int64 // 享受的折扣额（分）
	Overdue       bool  // 是否逾期（超过账期）
}

// SubmitResult 是发票提交/重新判定的结果。
type SubmitResult struct {
	Number     string
	Status     InvoiceStatus
	Amount     int64 // 放行金额；被保留时为 0
	ReleasedAt int64
	FailLine   int       // 被保留时首个失败行下标；放行时为 -1
	FailReason ErrorKind // 被保留时的行内原因
}
