package card

import (
	"math/big"
	"sort"
)

// interestUnit is the divisor that turns an accumulated
// (balance x rate-in-basis-points x days) base into interest:
// 10000 basis points x 365 days.
const interestUnit = 10000 * 365

// account is the mutable state of one credit-card account. It is not
// safe for concurrent use; the Ledger serializes all access.
type account struct {
	params Params

	bal  [NumCategories]int64 // balances per category, never negative
	over int64                // overpayment (credit balance)

	// Interest accrual state. accBase[c] accumulates
	// balance-at-start-of-day x rate over the days of the current
	// period; carry[c] is the sub-unit remainder carried over from
	// previous periods.
	lastAccrualDay int64
	accBase        [NumCategories]*big.Int
	carry          [NumCategories]int64

	bills         []*Bill
	unpaidMin     []int // stack of bill seqs with minRemaining > 0, newest on top
	prevFullyPaid bool  // whether the previous bill was fully repaid in its window

	repayments []*Repayment

	// payOrder orders categories by rate descending, ties broken by
	// category order (cash, installment, purchase).
	payOrder [NumCategories]int

	// accrualSteps counts lazy accrual updates, for cost verification.
	accrualSteps int64
}

func newAccount(params Params, createdDay int64) *account {
	a := &account{
		params:         params,
		lastAccrualDay: createdDay,
		prevFullyPaid:  true, // the first period is treated as fully paid
	}
	for c := range a.accBase {
		a.accBase[c] = new(big.Int)
	}
	order := [NumCategories]int{int(Cash), int(Installment), int(Purchase)}
	sort.SliceStable(order[:], func(i, j int) bool {
		return params.Rates[order[i]] > params.Rates[order[j]]
	})
	a.payOrder = order
	return a
}

// accrue advances interest accumulation up to and including day, using the
// balance at the start of each day (i.e. before that day's operations).
// Cost is O(1) per call: the balance only changes on operations, so all
// days since the last operation share the current balance.
func (a *account) accrue(day int64) {
	if day <= a.lastAccrualDay {
		return
	}
	days := day - a.lastAccrualDay
	for c := 0; c < NumCategories; c++ {
		if a.bal[c] == 0 || a.params.Rates[c] == 0 {
			continue
		}
		t := new(big.Int).SetInt64(a.bal[c])
		t.Mul(t, big.NewInt(a.params.Rates[c]))
		t.Mul(t, big.NewInt(days))
		a.accBase[c].Add(a.accBase[c], t)
	}
	a.accrualSteps++
	a.lastAccrualDay = day
}

// charge posts a purchase of amount to category cat. Overpayment offsets
// the charge first; only the remainder forms balance.
func (a *account) charge(day int64, cat Category, amount int64) {
	a.accrue(day)
	if a.over > 0 {
		use := min(a.over, amount)
		a.over -= use
		amount -= use
	}
	a.bal[cat] += amount
}

// repay accepts a repayment and allocates it immediately:
//  1. the unsatisfied minimum of the most recent unpaid bill, applied
//     to balances in the fixed order cash, installment, purchase;
//  2. the excess, applied by rate descending (ties in category order);
//  3. any remainder becomes overpayment.
func (a *account) repay(day int64, amount int64) Repayment {
	a.accrue(day)

	// Attribute the repayment to the newest bill's window, if still open.
	if n := len(a.bills); n > 0 {
		newest := a.bills[n-1]
		if day <= newest.DueDay {
			newest.PaidInWindow += amount
		}
	}

	var alloc [NumCategories]int64
	remaining := amount

	var toMin int64
	if len(a.unpaidMin) > 0 {
		top := a.bills[a.unpaidMin[len(a.unpaidMin)-1]]
		portion := min(remaining, top.minRemaining)
		top.minRemaining -= portion
		if top.minRemaining == 0 {
			a.unpaidMin = a.unpaidMin[:len(a.unpaidMin)-1]
		}
		toMin = portion
		left := portion
		for c := 0; c < NumCategories && left > 0; c++ {
			take := min(a.bal[c], left)
			a.bal[c] -= take
			alloc[c] += take
			left -= take
			remaining -= take
		}
		// If balances ran out before the designated portion was
		// consumed, the rest falls through to the excess path (and,
		// with zero balances, to overpayment).
	}

	for _, c := range a.payOrder {
		if remaining == 0 {
			break
		}
		take := min(a.bal[c], remaining)
		a.bal[c] -= take
		alloc[c] += take
		remaining -= take
	}

	a.over += remaining
	rec := &Repayment{
		Seq:    len(a.repayments),
		Day:    day,
		Amount: amount,
		Alloc:  alloc,
		ToMin:  toMin,
		Over:   remaining,
	}
	a.repayments = append(a.repayments, rec)
	return *rec
}

// bill generates a statement on day. It must be called only after the
// too-early check has passed.
func (a *account) bill(day int64) Bill {
	a.accrue(day)

	// 0. Decide this period's purchase-interest waiver and the late fee
	//    from the previous bill only (never from this period's own
	//    repayments). The first period keeps the initial fully-paid
	//    state.
	var fee int64
	if n := len(a.bills); n > 0 {
		prev := a.bills[n-1]
		a.prevFullyPaid = prev.PaidInWindow >= prev.Total
		if prev.PaidInWindow < prev.MinDue {
			fee = min(a.params.LateFeeCap, prev.MinDue-prev.PaidInWindow)
		}
	}

	// 1. Interest per category: (accumulated base + carried remainder)
	//    / (10000 x 365), floor; the remainder carries to the next
	//    period and is never lost. Purchase interest is waived when the
	//    previous bill was fully repaid in its window; the carried
	//    remainder is kept (not lost) even then.
	var interest [NumCategories]int64
	unit := big.NewInt(interestUnit)
	quotient, remainder := new(big.Int), new(big.Int)
	for c := 0; c < NumCategories; c++ {
		waived := c == int(Purchase) && a.prevFullyPaid
		if waived {
			a.accBase[c].SetInt64(0)
			continue
		}
		total := new(big.Int).Add(a.accBase[c], big.NewInt(a.carry[c]))
		quotient.QuoRem(total, unit, remainder)
		interest[c] = quotient.Int64()
		a.carry[c] = remainder.Int64()
		a.accBase[c].SetInt64(0)
	}

	// 2. Apply interest and late fee to balances.
	for c := 0; c < NumCategories; c++ {
		a.bal[c] += interest[c]
	}
	a.bal[Purchase] += fee

	// 3. Statement total and minimum payment.
	total := a.bal[Cash] + a.bal[Installment] + a.bal[Purchase]
	var minDue int64
	if total > 0 {
		interestSum := interest[Cash] + interest[Installment] + interest[Purchase]
		other := total - interestSum - fee
		// ceil(other x minPayRatio / 10000), overflow-safe
		scaled := new(big.Int).Mul(big.NewInt(other), big.NewInt(a.params.MinPayRatio))
		scaled.Add(scaled, big.NewInt(9999))
		base := scaled.Div(scaled, big.NewInt(10000)).Int64()
		minDue = interestSum + fee + base
		if minDue < a.params.MinPayFloor {
			minDue = a.params.MinPayFloor
		}
		if minDue > total {
			minDue = total
		}
	}

	b := &Bill{
		Seq:          len(a.bills),
		BillDay:      day,
		DueDay:       day + a.params.GraceDays,
		Total:        total,
		MinDue:       minDue,
		Interest:     interest,
		LateFee:      fee,
		minRemaining: minDue,
	}
	a.bills = append(a.bills, b)
	if minDue > 0 {
		a.unpaidMin = append(a.unpaidMin, b.Seq)
	}
	return *b
}

// tooEarly reports whether generating a statement on day would violate the
// "later than the previous bill's due day" rule.
func (a *account) tooEarly(day int64) bool {
	n := len(a.bills)
	return n > 0 && day <= a.bills[n-1].DueDay
}
