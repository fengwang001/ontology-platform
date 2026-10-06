package creditcard

import "math/big"

// account is the mutable state of one credit-card account. It is not safe
// for concurrent use on its own; Service serializes all access.
type account struct {
	params Params

	balance     [NumCategories]int64
	overpayment int64

	// accBase accumulates sum(balance * rateBps) over the days of the
	// current period; big.Int avoids overflow on huge inputs.
	accBase [NumCategories]big.Int
	// rem carries the sub-unit interest remainder across periods.
	rem [NumCategories]int64

	bills    []Bill
	payments []PaymentRecord

	// excessOrder lists categories by rate desc, ties broken Cash <
	// Installment < Purchase; used for the over-minimum part of payments.
	excessOrder [NumCategories]Category

	lastOpDay  int64 // day of the last accepted operation
	accrualDay int64 // next day whose interest base is not yet accrued
}

func newAccount(p Params, now int64) *account {
	a := &account{params: p, lastOpDay: now, accrualDay: now + 1}
	// Insertion sort the three categories by (rate desc, category asc).
	order := [NumCategories]Category{Cash, Installment, Purchase}
	for i := 1; i < NumCategories; i++ {
		for j := i; j > 0; j-- {
			ri, rj := p.RateBps[order[j]], p.RateBps[order[j-1]]
			if rj > ri || (rj == ri && order[j-1] < order[j]) {
				break
			}
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	a.excessOrder = order
	return a
}

// accrue folds the interest base of every day up to and including `now`
// into accBase, using the balances at the start of those days. It must be
// called before any state mutation of an accepted operation.
func (a *account) accrue(now int64) {
	if now <= a.lastOpDay {
		return // same day as the last op: today's base is already accrued
	}
	// Invariant: accrualDay == lastOpDay+1, so n >= 1. Days
	// accrualDay..now all see the current balances: no operation happened
	// before `now` since the last mutation, and the start-of-day balance
	// of `now` itself is the current one (no op of day `now` ran yet).
	n := now + 1 - a.accrualDay
	nBig := big.NewInt(n)
	for c := Category(0); c < NumCategories; c++ {
		if a.balance[c] == 0 || a.params.RateBps[c] == 0 {
			continue
		}
		t := new(big.Int).Mul(big.NewInt(a.balance[c]), big.NewInt(a.params.RateBps[c]))
		t.Mul(t, nBig)
		a.accBase[c].Add(&a.accBase[c], t)
	}
	a.accrualDay = now + 1
	a.lastOpDay = now
}

// charge adds amount to the given category, absorbing overpayment first.
func (a *account) charge(c Category, amount, now int64) {
	a.accrue(now)
	absorbed := min(a.overpayment, amount)
	a.overpayment -= absorbed
	a.balance[c] += amount - absorbed
}

// pay allocates amount immediately and records the allocation.
func (a *account) pay(amount, now int64) PaymentRecord {
	a.accrue(now)
	rec := PaymentRecord{Seq: len(a.payments) + 1, Day: now, Amount: amount}
	remaining := amount

	if len(a.bills) > 0 {
		cur := &a.bills[len(a.bills)-1]
		// Part 1: up to the still-unpaid minimum payment of the most
		// recent bill, applied in fixed Cash -> Installment -> Purchase
		// order. Any remainder falls through to the excess part.
		minPart := min(remaining, max(cur.MinPayment-cur.PaidTotal, 0))
		for c := Category(0); c < NumCategories && minPart > 0; c++ {
			x := min(minPart, a.balance[c])
			a.balance[c] -= x
			rec.Alloc[c] += x
			minPart -= x
			remaining -= x
		}
	}
	// Part 2: excess over the minimum payment, applied by rate desc.
	for _, c := range a.excessOrder {
		if remaining == 0 {
			break
		}
		x := min(remaining, a.balance[c])
		a.balance[c] -= x
		rec.Alloc[c] += x
		remaining -= x
	}
	if remaining > 0 {
		a.overpayment += remaining
		rec.Overpayment = remaining
	}

	if len(a.bills) > 0 {
		cur := &a.bills[len(a.bills)-1]
		cur.PaidTotal += amount
		if now <= cur.DueDate {
			cur.PaidByDueDate += amount
		}
	}
	a.payments = append(a.payments, rec)
	return rec
}

// bill closes the current period: charges interest and any late fee,
// computes the statement total and minimum payment, and starts a new
// period. The caller must have validated the billing-day rules.
func (a *account) bill(now int64) Bill {
	a.accrue(now)

	// Purchase interest is charged only if the previous bill was not
	// fully paid by its due date; the first period counts as paid.
	purchaseCharged := false
	if n := len(a.bills); n > 0 {
		prev := a.bills[n-1]
		purchaseCharged = prev.PaidByDueDate < prev.Total
	}

	var interest [NumCategories]int64
	var totalInterest int64
	den := big.NewInt(interestDenominator)
	for c := Category(0); c < NumCategories; c++ {
		if c == Purchase && !purchaseCharged {
			// Interest-free period: the per-period base is discarded,
			// the carried remainder is preserved.
			a.accBase[c].SetInt64(0)
			continue
		}
		sum := new(big.Int).Add(&a.accBase[c], big.NewInt(a.rem[c]))
		q, r := new(big.Int).QuoRem(sum, den, new(big.Int))
		interest[c] = q.Int64()
		a.rem[c] = r.Int64()
		a.accBase[c].SetInt64(0)
		a.balance[c] += interest[c]
		totalInterest += interest[c]
	}

	// Late fee for the previous bill, charged into the purchase balance.
	var lateFee int64
	if n := len(a.bills); n > 0 {
		prev := a.bills[n-1]
		if prev.PaidByDueDate < prev.MinPayment {
			lateFee = min(a.params.LateFeeCap, prev.MinPayment-prev.PaidByDueDate)
			a.balance[Purchase] += lateFee
		}
	}

	total := a.balance[Cash] + a.balance[Installment] + a.balance[Purchase]
	var minPay int64
	if total > 0 {
		base := total - totalInterest - lateFee
		minPay = totalInterest + lateFee + ceilMulDiv(base, a.params.MinPayRatioBps, 10000)
		minPay = max(minPay, a.params.MinPayFloor)
		minPay = min(minPay, total)
	}

	b := Bill{
		Seq:        len(a.bills) + 1,
		BillDay:    now,
		DueDate:    now + a.params.GraceDays,
		Total:      total,
		MinPayment: minPay,
		Interest:   interest,
		LateFee:    lateFee,
	}
	a.bills = append(a.bills, b)
	return b
}

// ceilMulDiv returns ceil(a*b/m) for non-negative inputs without overflow.
func ceilMulDiv(a, b, m int64) int64 {
	t := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	t.Add(t, big.NewInt(m-1))
	return t.Quo(t, big.NewInt(m)).Int64()
}
