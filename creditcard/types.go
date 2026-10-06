// Package creditcard implements a credit-card account engine: explicit
// billing, daily interest accrual with cross-period remainder carryover,
// payment allocation, late fees and overpayment handling.
//
// Amounts are non-negative integers in the smallest currency unit.
// Time is an integer day carried by every operation as `now`.
package creditcard

import "errors"

// Category identifies one of the three balance buckets.
type Category int

const (
	// Cash is the cash-advance balance (取现).
	Cash Category = iota
	// Installment is the installment balance (分期).
	Installment
	// Purchase is the purchase balance (购物).
	Purchase
)

// NumCategories is the fixed number of balance buckets.
const NumCategories = 3

// Valid reports whether c is one of the three known categories.
func (c Category) Valid() bool { return c >= Cash && c <= Purchase }

func (c Category) String() string {
	switch c {
	case Cash:
		return "cash"
	case Installment:
		return "installment"
	case Purchase:
		return "purchase"
	default:
		return "unknown"
	}
}

// Errors are distinguishable via errors.Is. When several apply, only the
// first by priority is reported:
// ErrInvalidParam > ErrClockRollback > ErrAccountNotFound > ErrBillingTooEarly.
var (
	ErrInvalidParam    = errors.New("creditcard: invalid parameter")
	ErrClockRollback   = errors.New("creditcard: clock rollback")
	ErrAccountNotFound = errors.New("creditcard: account not found")
	ErrAccountExists   = errors.New("creditcard: account already exists")
	ErrBillingTooEarly = errors.New("creditcard: billing too early")
)

// interestDenominator converts balance*basis-points*days into currency units.
const interestDenominator = 10000 * 365

// Params holds the immutable configuration of an account.
type Params struct {
	// RateBps is the annualized interest rate per category, in basis points.
	RateBps [NumCategories]int64
	// GraceDays G: bill due date = billing day + G (inclusive).
	GraceDays int64
	// MinPayRatioBps is the minimum-payment ratio in basis points.
	MinPayRatioBps int64
	// MinPayFloor M0: lower bound of the minimum payment.
	MinPayFloor int64
	// LateFeeCap F: upper bound of a single late fee.
	LateFeeCap int64
}

func (p Params) valid() bool {
	if p.GraceDays < 0 || p.MinPayFloor < 0 || p.LateFeeCap < 0 {
		return false
	}
	if p.MinPayRatioBps < 0 || p.MinPayRatioBps > 10000 {
		return false
	}
	for _, r := range p.RateBps {
		if r < 0 {
			return false
		}
	}
	return true
}

// Bill is one statement record.
type Bill struct {
	Seq        int   // 1-based statement sequence number
	BillDay    int64 // billing (出账) day
	DueDate    int64 // BillDay + GraceDays, inclusive
	Total      int64 // statement total: sum of the three balances at billing
	MinPayment int64 // minimum payment due
	// Interest is the per-category interest charged at this billing.
	Interest [NumCategories]int64
	// LateFee is the late fee charged at this billing (for the previous bill).
	LateFee int64
	// PaidTotal is the sum of payments accepted after this billing.
	PaidTotal int64
	// PaidByDueDate is the sum of payments accepted after this billing with
	// acceptance day <= DueDate; it decides late fee and interest-free grace.
	PaidByDueDate int64
}

// InterestTotal returns the summed interest over all categories.
func (b Bill) InterestTotal() int64 {
	return b.Interest[Cash] + b.Interest[Installment] + b.Interest[Purchase]
}

// PaymentRecord describes how one accepted payment was allocated.
type PaymentRecord struct {
	Seq    int   // 1-based payment sequence number on the account
	Day    int64 // acceptance day
	Amount int64 // total accepted amount
	// Alloc is the amount applied to each category balance.
	Alloc [NumCategories]int64
	// Overpayment is the part exceeding all balances.
	Overpayment int64
}
