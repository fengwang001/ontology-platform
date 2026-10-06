package booking

type RefundResult struct {
	TicketID      string
	At            int64
	Tier          string
	Rate          int
	CurrentPrice  int64
	Fee           int64
	Refund        int64
	PaidChangeFee int64
	CashRefund    int64
	Involuntary   bool
	Basis         string
}

type ChangeResult struct {
	TicketID           string
	At                 int64
	Tier               string
	Rate               int
	ChangeFee          int64
	PriceDifference    int64
	GrossDue           int64
	VoucherID          string
	VoucherDeduction   int64
	CashDue            int64
	GeneratedVoucherID string
	GeneratedVoucher   int64
	NewPrice           int64
	NewDepartAt        int64
	Changes            int
	Involuntary        bool
	Basis              string
}
