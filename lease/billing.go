package lease

import "sort"

// billing owns monthly installments, arrears raising and settlement.
//
// Time model: every month is exactly 30 days. Month m (m >= 0) covers
// [30m, 30m+30); its installment is due on day 30m + payDay. A lease owes an
// installment for month m exactly when its [start,end) intersects that month.
// An unpaid installment becomes an arrears at day due + G (inclusive).

func dueDay(month, payDay int) int { return 30*month + payDay }

// billMonths returns the [first,last] month indices whose installment due day
// (30m + payDay) falls inside [start,end): those are the installments a lease
// ever owes. Months that intersect the term but whose due day precedes the
// start produce no bill.
func billMonths(start, end, payDay int) (int, int) {
	if end <= start {
		return 1, 0 // empty range
	}
	first := start / 30
	if dueDay(first, payDay) < start {
		first++
	}
	last := (end - 1) / 30
	if dueDay(last, payDay) >= end {
		last--
	}
	return first, last
}

// raiseArrears creates newly-overdue installments for every active/past lease.
func (w *world) raiseArrears(now int) {
	var ids []LeaseID
	for id := range w.leases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		l := w.leases[id]
		first, last := billMonths(l.Start, l.End, w.cfg.PayDay)
		for m := first; m <= last; m++ {
			due := dueDay(m, w.cfg.PayDay)
			if now < due+w.cfg.GraceDays || w.hasArrears(l.ID, due) {
				continue
			}
			idn := w.nextArrearsID
			w.nextArrearsID++
			w.arrears = append(w.arrears, &Arrears{
				ID:     idn,
				Lease:  l.ID,
				Root:   l.Root,
				Due:    due,
				Amount: l.Rent,
				Chain:  liabilityChain(w.leases, l.ID),
			})
			w.arrearsByID[idn] = w.arrears[len(w.arrears)-1]
		}
	}
}

func (w *world) hasArrears(id LeaseID, due int) bool {
	for _, a := range w.arrears {
		if a.Lease == id && a.Due == due {
			return true
		}
	}
	return false
}

// settle applies one payment against an arrears, creating one recourse.
// Joint liability: the debtor and every ancestor at creation time may pay.
// Payment by an ancestor discharges the same debt for every level below it;
// the settling level gets one recourse claim of exactly the paid amount
// against the actual debtor. A fully paid arrears cannot be paid again.
func (w *world) settle(a *Arrears, op PayOp) error {
	if op.Amount <= 0 {
		return fail(ErrInvalid)
	}
	if a.Paid >= a.Amount {
		return fail(ErrStateNotAllowed)
	}
	levelIdx := -1
	for i, id := range a.Chain {
		if id == op.AtLease {
			levelIdx = i
		}
	}
	if levelIdx < 0 {
		return fail(ErrStateNotAllowed)
	}
	if w.leases[op.AtLease].Tenant != op.By {
		return fail(ErrStateNotAllowed)
	}
	if op.Amount > a.Amount-a.Paid {
		return fail(ErrInvalid)
	}
	a.Paid += op.Amount
	a.Payments = append(a.Payments, Payment{
		Payer: op.By, Lease: op.AtLease, Amount: op.Amount, Day: op.Now,
	})
	w.recourse = append(w.recourse, Recourse{
		ArrearsID: a.ID,
		From:      w.leases[a.Lease].Tenant,
		To:        op.By,
		Amount:    op.Amount,
		Day:       op.Now,
	})
	return nil
}
