package creditcard

// naiveService is an independent, deliberately unoptimized reference
// implementation of the same rules, used only by the differential test.
// It recomputes interest by literally iterating every day of the period
// and summing start-of-day balances replayed from an event log.

import "math/big"

type naiveEvent struct {
	day   int64
	delta [NumCategories]int64
}

type naiveAccount struct {
	p           Params
	bal         [NumCategories]int64
	over        int64
	events      []naiveEvent
	rem         [NumCategories]int64
	bills       []Bill
	pays        []PaymentRecord
	openDay     int64
	lastBillDay int64
	hasBill     bool
}

type naiveService struct {
	clock    int64
	clockSet bool
	accounts map[string]*naiveAccount
}

func newNaiveService() *naiveService {
	return &naiveService{accounts: make(map[string]*naiveAccount)}
}

func (s *naiveService) checkClock(now int64) error {
	if s.clockSet && now < s.clock {
		return ErrClockRollback
	}
	return nil
}

func (s *naiveService) accept(now int64) {
	s.clock = now
	s.clockSet = true
}

func (s *naiveService) create(id string, p Params, now int64) error {
	if id == "" || !p.valid() {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.accounts[id]; ok {
		return ErrAccountExists
	}
	s.accounts[id] = &naiveAccount{p: p, openDay: now}
	s.accept(now)
	return nil
}

func (s *naiveService) charge(id string, c Category, amount, now int64) error {
	if amount <= 0 || !c.Valid() {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	a, ok := s.accounts[id]
	if !ok {
		return ErrAccountNotFound
	}
	absorbed := min(a.over, amount)
	a.over -= absorbed
	var d [NumCategories]int64
	d[c] = amount - absorbed
	a.bal[c] += d[c]
	a.events = append(a.events, naiveEvent{day: now, delta: d})
	s.accept(now)
	return nil
}

func (s *naiveService) pay(id string, amount, now int64) (PaymentRecord, error) {
	if amount <= 0 {
		return PaymentRecord{}, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return PaymentRecord{}, err
	}
	a, ok := s.accounts[id]
	if !ok {
		return PaymentRecord{}, ErrAccountNotFound
	}
	rec := PaymentRecord{Seq: len(a.pays) + 1, Day: now, Amount: amount}
	remaining := amount
	var d [NumCategories]int64

	if a.hasBill {
		cur := &a.bills[len(a.bills)-1]
		minPart := min(remaining, max(cur.MinPayment-cur.PaidTotal, 0))
		for c := Category(0); c < NumCategories && minPart > 0; c++ {
			x := min(minPart, a.bal[c])
			a.bal[c] -= x
			d[c] -= x
			rec.Alloc[c] += x
			minPart -= x
			remaining -= x
		}
	}
	// excess: rate desc, ties cash < installment < purchase
	for round := 0; round < NumCategories && remaining > 0; round++ {
		best := Category(-1)
		for c := Category(0); c < NumCategories; c++ {
			if a.bal[c] == 0 {
				continue
			}
			if best == -1 || a.p.RateBps[c] > a.p.RateBps[best] {
				best = c
			}
		}
		if best == -1 {
			break
		}
		x := min(remaining, a.bal[best])
		a.bal[best] -= x
		d[best] -= x
		rec.Alloc[best] += x
		remaining -= x
	}
	if remaining > 0 {
		a.over += remaining
		rec.Overpayment = remaining
	}
	if a.hasBill {
		cur := &a.bills[len(a.bills)-1]
		cur.PaidTotal += amount
		if now <= cur.DueDate {
			cur.PaidByDueDate += amount
		}
	}
	a.events = append(a.events, naiveEvent{day: now, delta: d})
	a.pays = append(a.pays, rec)
	s.accept(now)
	return rec, nil
}

func (s *naiveService) bill(id string, now int64) (Bill, error) {
	if err := s.checkClock(now); err != nil {
		return Bill{}, err
	}
	a, ok := s.accounts[id]
	if !ok {
		return Bill{}, ErrAccountNotFound
	}
	if a.hasBill && now <= a.bills[len(a.bills)-1].DueDate {
		return Bill{}, ErrBillingTooEarly
	}

	// Interest base per category: iterate every day of the period and
	// replay the event log to obtain the start-of-day balance.
	start := a.openDay + 1
	if a.hasBill {
		start = a.lastBillDay + 1
	}
	var base [NumCategories]big.Int
	var cum [NumCategories]int64
	idx := 0
	for d := start; d <= now; d++ {
		for idx < len(a.events) && a.events[idx].day < d {
			for c := Category(0); c < NumCategories; c++ {
				cum[c] += a.events[idx].delta[c]
			}
			idx++
		}
		for c := Category(0); c < NumCategories; c++ {
			if cum[c] != 0 && a.p.RateBps[c] != 0 {
				t := new(big.Int).Mul(big.NewInt(cum[c]), big.NewInt(a.p.RateBps[c]))
				base[c].Add(&base[c], t)
			}
		}
	}

	purchaseCharged := false
	if a.hasBill {
		prev := a.bills[len(a.bills)-1]
		purchaseCharged = prev.PaidByDueDate < prev.Total
	}

	var interest [NumCategories]int64
	var totalInterest int64
	den := big.NewInt(interestDenominator)
	for c := Category(0); c < NumCategories; c++ {
		if c == Purchase && !purchaseCharged {
			continue
		}
		sum := new(big.Int).Add(&base[c], big.NewInt(a.rem[c]))
		q, r := new(big.Int).QuoRem(sum, den, new(big.Int))
		interest[c] = q.Int64()
		a.rem[c] = r.Int64()
		a.bal[c] += interest[c]
		totalInterest += interest[c]
	}

	var lateFee int64
	if a.hasBill {
		prev := a.bills[len(a.bills)-1]
		if prev.PaidByDueDate < prev.MinPayment {
			lateFee = min(a.p.LateFeeCap, prev.MinPayment-prev.PaidByDueDate)
			a.bal[Purchase] += lateFee
		}
	}

	total := a.bal[Cash] + a.bal[Installment] + a.bal[Purchase]
	var minPay int64
	if total > 0 {
		rest := total - totalInterest - lateFee
		// ceil(rest * ratio / 10000) with big.Int arithmetic
		t := new(big.Int).Mul(big.NewInt(rest), big.NewInt(a.p.MinPayRatioBps))
		t.Add(t, big.NewInt(9999))
		t.Quo(t, big.NewInt(10000))
		minPay = totalInterest + lateFee + t.Int64()
		minPay = max(minPay, a.p.MinPayFloor)
		minPay = min(minPay, total)
	}

	b := Bill{
		Seq:        len(a.bills) + 1,
		BillDay:    now,
		DueDate:    now + a.p.GraceDays,
		Total:      total,
		MinPayment: minPay,
		Interest:   interest,
		LateFee:    lateFee,
	}
	a.bills = append(a.bills, b)
	a.lastBillDay = now
	a.hasBill = true

	// Record interest and late fee as balance events on the billing day.
	var d [NumCategories]int64
	for c := Category(0); c < NumCategories; c++ {
		d[c] += interest[c]
	}
	d[Purchase] += lateFee
	a.events = append(a.events, naiveEvent{day: now, delta: d})

	s.accept(now)
	return b, nil
}
