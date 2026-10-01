package loan

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrNotAmortizable   = errors.New("loan is not amortizable")
	ErrTermExceeded     = errors.New("term limit exceeded")
	ErrSettled          = errors.New("loan is settled")
	ErrOverpayment      = errors.New("prepayment exceeds balance")
	ErrBelowMinimum     = errors.New("prepayment is below minimum")
	ErrRatePeriodPassed = errors.New("rate change period has passed")
	ErrHolidayActive    = errors.New("payment holiday is already active")
)

const (
	maxPeriods       = 600
	simulatedPeriods = 601
)

type Loan struct {
	mu sync.RWMutex

	bal int64
	k   int64
	r   int64
	a   int64

	rateChanges map[int64]int64
	h           int
}

type snapshot struct {
	bal         int64
	k           int64
	r           int64
	a           int64
	rateChanges map[int64]int64
	h           int
}

type Payment struct {
	Period    int64
	Amount    int64
	Interest  int64
	Principal int64
	Balance   int64
}

type Prepayment struct {
	Amount  int64
	Fee     int64
	Balance int64
}

func New(principal int64, monthlyRate int64, payment int64) (*Loan, error) {
	if principal < 1 || principal > 1_000_000_000_000 ||
		monthlyRate < 1 || monthlyRate > 1_000_000 ||
		payment < 1 || payment > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	l := &Loan{
		bal:         principal,
		r:           monthlyRate,
		a:           payment,
		rateChanges: make(map[int64]int64),
	}
	if err := validate(l.snapshot()); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Loan) Pay() (Payment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.bal == 0 {
		return Payment{}, ErrSettled
	}

	s := l.snapshot()
	payment, err := advance(&s)
	if err != nil {
		return Payment{}, err
	}
	l.bal = s.bal
	l.k = s.k
	l.h = s.h
	return payment, nil
}

func (l *Loan) Prepay(amount int64) (Prepayment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if amount < 1 || amount > 1_000_000_000_000 {
		return Prepayment{}, ErrInvalidArgument
	}
	if l.bal == 0 {
		return Prepayment{}, ErrSettled
	}
	if amount > l.bal {
		return Prepayment{}, ErrOverpayment
	}
	if amount < l.a && amount != l.bal {
		return Prepayment{}, ErrBelowMinimum
	}

	fee := prepaymentFee(amount, l.k)
	l.bal -= amount
	return Prepayment{
		Amount:  amount,
		Fee:     fee,
		Balance: l.bal,
	}, nil
}

func (l *Loan) SetRate(rate int64, period int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if rate < 1 || rate > 1_000_000 || period < 1 || period > 1000 {
		return ErrInvalidArgument
	}
	if l.bal == 0 {
		return ErrSettled
	}
	if period <= l.k {
		return ErrRatePeriodPassed
	}

	s := l.snapshot()
	s.rateChanges[period] = rate
	if err := validate(s); err != nil {
		return err
	}
	l.rateChanges[period] = rate
	return nil
}

func (l *Loan) Holiday(count int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if count < 1 || count > 12 {
		return ErrInvalidArgument
	}
	if l.bal == 0 {
		return ErrSettled
	}
	if l.h > 0 {
		return ErrHolidayActive
	}

	s := l.snapshot()
	s.h = count
	if err := validate(s); err != nil {
		return err
	}
	l.h = count
	return nil
}

func (l *Loan) Remaining() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return remaining(l.snapshot())
}

func (l *Loan) Balance() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.bal
}

func (l *Loan) PaidPeriods() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.k
}

func (l *Loan) snapshot() snapshot {
	return snapshot{
		bal:         l.bal,
		k:           l.k,
		r:           l.r,
		a:           l.a,
		rateChanges: cloneRates(l.rateChanges),
		h:           l.h,
	}
}

func cloneRates(rates map[int64]int64) map[int64]int64 {
	copied := make(map[int64]int64, len(rates))
	for period, rate := range rates {
		copied[period] = rate
	}
	return copied
}

func rateAt(s snapshot, period int64) int64 {
	rate := s.r
	var changePeriod int64
	for candidatePeriod, candidateRate := range s.rateChanges {
		if candidatePeriod <= period && candidatePeriod > changePeriod {
			changePeriod = candidatePeriod
			rate = candidateRate
		}
	}
	return rate
}

func interestFor(balance int64, rate int64) int64 {
	return (balance*rate + 500_000) / 1_000_000
}

func advance(s *snapshot) (Payment, error) {
	period := s.k + 1
	interest := interestFor(s.bal, rateAt(*s, period))
	payment := Payment{
		Period:   period,
		Interest: interest,
	}

	if s.h > 0 {
		payment.Amount = interest
		s.h--
	} else if s.bal+interest <= s.a {
		payment.Amount = s.bal + interest
		payment.Principal = s.bal
		s.bal = 0
	} else {
		if s.a <= interest {
			return Payment{}, ErrNotAmortizable
		}
		payment.Amount = s.a
		payment.Principal = s.a - interest
		s.bal -= payment.Principal
	}

	payment.Balance = s.bal
	s.k++
	return payment, nil
}

func validate(s snapshot) error {
	for period := int64(1); period <= simulatedPeriods; period++ {
		if _, err := advance(&s); err != nil {
			return err
		}
		if s.bal == 0 {
			if period <= maxPeriods {
				return nil
			}
			return ErrTermExceeded
		}
	}
	return ErrTermExceeded
}

func remaining(s snapshot) int64 {
	if s.bal == 0 {
		return 0
	}
	for period := int64(1); period <= maxPeriods; period++ {
		if _, err := advance(&s); err != nil {
			return maxPeriods
		}
		if s.bal == 0 {
			return period
		}
	}
	return maxPeriods
}

func prepaymentFee(amount int64, paidPeriods int64) int64 {
	var basisPoints int64
	switch {
	case paidPeriods < 12:
		basisPoints = 300
	case paidPeriods < 36:
		basisPoints = 100
	default:
		basisPoints = 0
	}
	return amount * basisPoints / 10_000
}
