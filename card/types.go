// Package card implements a credit-card account ledger: charges, statement
// generation (billing), interest accrual, repayment allocation, late fees
// and overpayment handling.
//
// Amounts are non-negative integers in the smallest currency unit.
// Time is an integer day carried by every operation ("now"); an operation
// whose now is smaller than the last accepted operation's now is rejected
// with ErrClockRollback. Rejected operations never change state or the clock.
package card

// Category identifies one of the three balance buckets.
type Category int

const (
	// Cash is the cash-advance balance; always interest-bearing.
	Cash Category = iota
	// Installment is the installment balance; always interest-bearing.
	Installment
	// Purchase is the purchase balance; interest-bearing only when the
	// previous statement was not fully repaid by its due day.
	Purchase
)

// NumCategories is the number of balance buckets.
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

// Params holds the immutable configuration of an account.
type Params struct {
	// Rates is the annual interest rate per category, in basis points
	// (1/100 of a percent). Non-negative.
	Rates [NumCategories]int64
	// GraceDays G: the statement due day is BillDay + GraceDays.
	GraceDays int64
	// MinPayRatio is the minimum-payment ratio in basis points,
	// applied to the non-interest/non-late-fee part of the statement.
	// Must be in [0, 10000].
	MinPayRatio int64
	// MinPayFloor M0: the minimum payment is never smaller than this
	// (unless the statement total is smaller, or zero).
	MinPayFloor int64
	// LateFeeCap F: the late fee never exceeds this amount.
	LateFeeCap int64
}

func (p Params) valid() bool {
	if p.GraceDays < 0 || p.MinPayFloor < 0 || p.LateFeeCap < 0 {
		return false
	}
	if p.MinPayRatio < 0 || p.MinPayRatio > 10000 {
		return false
	}
	for _, r := range p.Rates {
		if r < 0 {
			return false
		}
	}
	return true
}

// Bill is one generated statement. All fields are immutable once created.
type Bill struct {
	// Seq is the statement sequence number, starting at 0.
	Seq int
	// BillDay is the day the statement was generated.
	BillDay int64
	// DueDay is BillDay + GraceDays (inclusive repayment deadline).
	DueDay int64
	// Total is the statement total: the sum of the three balances at
	// billing time, including this period's interest and late fee.
	Total int64
	// MinDue is the minimum payment due for this statement.
	MinDue int64
	// Interest is this period's interest per category.
	Interest [NumCategories]int64
	// LateFee is the late fee charged on this statement (for the
	// previous statement's unpaid minimum).
	LateFee int64
	// PaidInWindow is the sum of accepted repayments in this bill's
	// repayment window (after its billing op, up to and including
	// DueDay). Informational; used for full-payment and late-fee
	// decisions.
	PaidInWindow int64

	// minRemaining tracks how much of MinDue is still unsatisfied for
	// repayment-allocation purposes.
	minRemaining int64
}

// Repayment records how one accepted repayment was allocated.
type Repayment struct {
	// Seq is the repayment sequence number, starting at 0.
	Seq int
	// Day is the acceptance day.
	Day int64
	// Amount is the total repayment amount.
	Amount int64
	// Alloc is the amount applied to each balance category.
	Alloc [NumCategories]int64
	// ToMin is the portion designated to the most recent unpaid
	// statement's minimum payment (allocated cash -> installment ->
	// purchase).
	ToMin int64
	// Over is the amount that exceeded all balances and became
	// overpayment.
	Over int64
}

// Snapshot is a read-only view of an account's balances.
type Snapshot struct {
	// Balances is the current balance per category.
	Balances [NumCategories]int64
	// Overpayment is the current overpayment (credit) amount.
	Overpayment int64
}
