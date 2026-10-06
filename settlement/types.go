package settlement

import "math"

type AccountID string
type SecurityID string

type Config struct {
	BusinessDays []int
	MaxFailDays  int
	PenaltyBPS   int64
}

type Account struct {
	ID       AccountID
	Cash     int64
	Holdings map[SecurityID]int64
}

type OrderInput struct {
	ID            uint64
	Security      SecurityID
	Buyer         AccountID
	Seller        AccountID
	Quantity      int64
	Price         int64
	SettlementDay int
	AllowPartial  bool
}

type OrderStatus string

const (
	StatusPending   OrderStatus = "pending"
	StatusPartial   OrderStatus = "partial"
	StatusCompleted OrderStatus = "completed"
	StatusForced    OrderStatus = "forced"
)

type ResponsibleParty string

const (
	ResponsibleBuyer  ResponsibleParty = "buyer"
	ResponsibleSeller ResponsibleParty = "seller"
)

type Order struct {
	Input      OrderInput
	Delivered  int64
	Remaining  int64
	Status     OrderStatus
	FailedDays int
}

type Position struct {
	Cash     int64
	Holdings map[SecurityID]int64
}

type Balances struct {
	Payable    int64
	Receivable int64
}

type OrderView struct {
	ID            uint64
	Security      SecurityID
	Buyer         AccountID
	Seller        AccountID
	Quantity      int64
	Price         int64
	SettlementDay int
	AllowPartial  bool
	Delivered     int64
	Remaining     int64
	Status        OrderStatus
	FailedDays    int
}

type FillResult struct {
	OrderID             uint64
	Security            SecurityID
	Buyer               AccountID
	Seller              AccountID
	Requested           int64
	Delivered           int64
	Remaining           int64
	SellerAvailable     int64
	BuyerAffordable     int64
	SettlementAmount    int64
	ResponsibleIfFailed ResponsibleParty
}

type PenaltyResult struct {
	OrderID      uint64
	Responsible  AccountID
	Counterparty AccountID
	Remaining    int64
	CashAmount   int64
	PenaltyBPS   int64
	Amount       int64
}

type ForceCloseResult struct {
	OrderID            uint64
	Remaining          int64
	Responsible        ResponsibleParty
	ReferencePrice     int64
	CompensationAmount int64
	Payer              AccountID
	Payee              AccountID
}

type ProcessResult struct {
	Day           int
	Processed     []FillResult
	Penalties     []PenaltyResult
	ForceClosures []ForceCloseResult
}

const maxInt = int64(math.MaxInt64)
