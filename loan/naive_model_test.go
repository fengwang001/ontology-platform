package loan

import "testing"

func newNaive(principal int64, rate int64, payment int64) (*naiveLoan, error) {
	if !inRange(principal, 1, 1_000_000_000_000) ||
		!inRange(rate, 1, 1_000_000) ||
		!inRange(payment, 1, 1_000_000_000_000) {
		return nil, ErrInvalidArgument
	}

	model := &naiveLoan{
		principal:   principal,
		balance:     principal,
		initialRate: rate,
		payment:     payment,
		rates:       make(map[int64]int64),
	}
	model.principalPaid = 0
	model.prepaid = 0
	if err := model.validate(); err != nil {
		return nil, err
	}
	return model, nil
}

func (m *naiveLoan) pay() (Payment, error) {
	if m.balance == 0 {
		return Payment{}, ErrClosedLoan
	}

	period := m.paidPeriods + 1
	interest := m.interestAt(period)
	result := Payment{Period: period, Interest: interest}

	if m.holidayLeft > 0 {
		result.Amount = interest
		m.holidayLeft--
	} else if m.balance+interest <= m.payment {
		result.Amount = m.balance + interest
		result.Principal = m.balance
		m.balance = 0
	} else {
		result.Amount = m.payment
		result.Principal = m.payment - interest
		m.balance -= result.Principal
	}

	m.principalPaid += result.Principal
	m.paidPeriods++
	result.Balance = m.balance
	return result, nil
}

func (m *naiveLoan) prepay(amount int64) (Prepayment, error) {
	if !inRange(amount, 1, 1_000_000_000_000) {
		return Prepayment{}, ErrInvalidArgument
	}
	if m.balance == 0 {
		return Prepayment{}, ErrClosedLoan
	}
	if amount > m.balance {
		return Prepayment{}, ErrOverMaxPrepay
	}
	if amount < m.payment && amount != m.balance {
		return Prepayment{}, ErrSmallPrepay
	}

	basisPoints := int64(300)
	switch {
	case m.paidPeriods >= 36:
		basisPoints = 0
	case m.paidPeriods >= 12:
		basisPoints = 100
	}
	fee := amount * basisPoints / 10_000

	m.balance -= amount
	m.prepaid += amount
	return Prepayment{Amount: amount, Fee: fee, Balance: m.balance}, nil
}

func (m *naiveLoan) setRate(rate int64, effectivePeriod int64) error {
	if !inRange(rate, 1, 1_000_000) || !inRange(effectivePeriod, 1, 1_000) {
		return ErrInvalidArgument
	}
	if m.balance == 0 {
		return ErrClosedLoan
	}
	if effectivePeriod <= m.paidPeriods {
		return ErrRateExpired
	}

	candidate := m.clone()
	candidate.rates[effectivePeriod] = rate
	if err := candidate.validate(); err != nil {
		return err
	}

	m.rates = candidate.rates
	return nil
}

func (m *naiveLoan) holiday(count int64) error {
	if !inRange(count, 1, 12) {
		return ErrInvalidArgument
	}
	if m.balance == 0 {
		return ErrClosedLoan
	}
	if m.holidayLeft > 0 {
		return ErrHolidayExists
	}

	candidate := m.clone()
	candidate.holidayLeft = count
	if err := candidate.validate(); err != nil {
		return err
	}

	m.holidayLeft = count
	return nil
}

func (m *naiveLoan) validate() error {
	candidate := m.clone()
	for step := int64(1); step <= 601; step++ {
		period := candidate.paidPeriods + 1
		interest := candidate.interestAt(period)

		if candidate.holidayLeft > 0 {
			candidate.holidayLeft--
		} else if candidate.balance+interest <= candidate.payment {
			if step == 601 {
				return ErrTermExceeded
			}
			return nil
		} else if candidate.payment <= interest {
			return ErrNotAmortizable
		} else {
			candidate.balance -= candidate.payment - interest
		}

		candidate.paidPeriods++
	}
	return ErrTermExceeded
}

func (m *naiveLoan) remaining() int64 {
	candidate := m.clone()
	count := int64(0)
	for candidate.balance > 0 {
		if _, err := candidate.pay(); err != nil {
			return -1
		}
		count++
	}
	return count
}

func (m *naiveLoan) interestAt(period int64) int64 {
	return (m.balance*m.rateAt(period) + 500_000) / 1_000_000
}

func (m *naiveLoan) rateAt(period int64) int64 {
	rate := m.initialRate
	latest := int64(0)
	for effectivePeriod, candidateRate := range m.rates {
		if effectivePeriod <= period && effectivePeriod >= latest {
			latest = effectivePeriod
			rate = candidateRate
		}
	}
	return rate
}

func (m *naiveLoan) clone() *naiveLoan {
	clone := *m
	clone.rates = make(map[int64]int64, len(m.rates))
	for period, rate := range m.rates {
		clone.rates[period] = rate
	}
	return &clone
}

func assertStateMatchesNaive(t *testing.T, actual *Loan, model *naiveLoan) {
	t.Helper()
	actual.mu.RLock()
	defer actual.mu.RUnlock()

	if actual.state.balance != model.balance ||
		actual.state.paidPeriods != model.paidPeriods ||
		actual.state.holidayLeft != model.holidayLeft ||
		actual.state.initialRate != model.initialRate ||
		actual.state.payment != model.payment ||
		len(actual.state.rates) != len(model.rates) {
		t.Fatalf("state mismatch: actual=%+v naive=%+v", actual.state, model)
	}
	for period, rate := range model.rates {
		if actual.state.rates[period] != rate {
			t.Fatalf("rate at period %d = %d, naive = %d", period, actual.state.rates[period], rate)
		}
	}
	if model.principalPaid+model.prepaid+model.balance != model.principal {
		t.Fatalf("principal conservation failed: paid=%d prepaid=%d balance=%d initial=%d",
			model.principalPaid, model.prepaid, model.balance, model.principal)
	}
	if model.balance > 0 && model.remaining() > 600 {
		t.Fatalf("naive Remaining = %d, want <= 600", model.remaining())
	}
}
