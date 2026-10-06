package deposit

import "math/big"

// Category is the fixed priority class of a deduction.  Numeric values follow
// the compensation order: Rent < Damage < Cleaning < Other.
type Category int

const (
	Rent Category = iota
	Damage
	Cleaning
	Other
)

// Config holds the three statutory day limits and the fixed daily penalty
// rate (numerator/denominator, e.g. 1/1000 = 0.1% per day).
type Config struct {
	A, B, C int
	RateNum int64
	RateDen int64
}

// Deduction is one declared deduction line.
type Deduction struct {
	ID          int
	Category    Category
	Amount      int64
	Declared    int
	Revoked     bool
	Disputed    bool
	Adjudicated bool
	// Awarded is the adjudicated amount; -1 before adjudication.
	Awarded int64
	// Satisfied is the deposit-allocated amount fixed at declaration close
	// (0 for items that never fit within the deposit).
	Satisfied int64
}

// RefundRecord is one completed full refund.
type RefundRecord struct {
	Day     int
	Amount  int64
	Penalty *big.Rat
}

// Snapshot is the externally visible state of a lease at a given day.
type Snapshot struct {
	Now         int
	Deposit     int64
	CheckoutDay int
	Refunded    int64
	Landlord    int64
	Frozen      int64
	Awaiting    int64
	Pending     int64
	Receivable  int64
	Refundable  int64
	PenaltyDue  *big.Rat
	Deductions  []Deduction
	Refunds     []RefundRecord
}

// Service is the concurrency-safe deposit dispute service.
type Service struct {
	mu     any
	cfg    Config
	leases map[string]*lease
}
