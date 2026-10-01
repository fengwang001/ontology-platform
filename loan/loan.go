package loan

import (
	"errors"
	"sync"
)

type ErrorCode string

const (
	ErrorCodeInvalidArgument = "invalid_argument"
	ErrorCodeClosed          = "closed"
	ErrorCodeNotAmortizable  = "not_amortizable"
	ErrorCodeTermExceeded    = "term_exceeded"
	ErrorCodeOverpayment     = "prepayment_too_large"
	ErrorCodeBelowMinimum    = "prepayment_too_small"
	ErrorCodeRateInPast      = "rate_effective_period_passed"
	ErrorCodeHolidayActive   = "holiday_already_active"
)

type LoanError struct {
	Code ErrorCode
	msg  string
}

func (e LoanError) Error() string { return e.msg }

var (
	ErrInvalidArgument = LoanError{Code: ErrorCodeInvalidArgument, msg: "invalid argument"}
	ErrClosedLoan      = LoanError{Code: ErrorCodeClosed, msg: "loan is closed"}
	ErrNotAmortizable  = LoanError{Code: ErrorCodeNotAmortizable, msg: "loan is not amortizable"}
	ErrTermExceeded    = LoanError{Code: ErrorCodeTermExceeded, msg: "loan term exceeds 600 periods"}
	ErrOverMaxPrepay   = LoanError{Code: ErrorCodeOverpayment, msg: "prepayment exceeds outstanding balance"}
	ErrSmallPrepay     = LoanError{Code: ErrorCodeBelowMinimum, msg: "prepayment is below monthly payment"}
	ErrRateExpired     = LoanError{Code: ErrorCodeRateInPast, msg: "rate effective period has already passed"}
	ErrHolidayExists   = LoanError{Code: ErrorCodeHolidayActive, msg: "a holiday schedule is already active"}
)

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

type Loan struct {
	mu    sync.RWMutex
	state loanState
}

type loanState struct {
	balance     int64
	paidPeriods int64
	initialRate int64
	payment     int64
	rates       map[int64]int64
	holidayLeft int64
}

func New(principal int64, monthlyRate int64, payment int64) (*Loan, error) {
	if !inRange(principal, 1, 1_000_000_000_000) ||
		!inRange(monthlyRate, 1, 1_000_000) ||
		!inRange(payment, 1, 1_000_000_000_000) {
		return nil, ErrInvalidArgument
	}

	initial := loanState{
		balance:     principal,
		initialRate: monthlyRate,
		payment:     payment,
		rates:       make(map[int64]int64),
	}
	if err := validateSchedule(initial); err != nil {
		return nil, err
	}

	return &Loan{state: initial}, nil
}

func (l *Loan) Pay() (Payment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state.balance == 0 {
		return Payment{}, ErrClosedLoan
	}

	period := l.state.paidPeriods + 1
	interest := interestFor(l.state, period)
	result := Payment{Period: period, Interest: interest}

	if l.state.holidayLeft > 0 {
		result.Amount = interest
		l.state.holidayLeft--
	} else if l.state.balance+interest <= l.state.payment {
		result.Amount = l.state.balance + interest
		result.Principal = l.state.balance
		l.state.balance = 0
	} else {
		result.Amount = l.state.payment
		result.Principal = l.state.payment - interest
		l.state.balance -= result.Principal
	}

	l.state.paidPeriods++
	result.Balance = l.state.balance
	return result, nil
}

func (l *Loan) Prepay(amount int64) (Prepayment, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !inRange(amount, 1, 1_000_000_000_000) {
		return Prepayment{}, ErrInvalidArgument
	}
	if l.state.balance == 0 {
		return Prepayment{}, ErrClosedLoan
	}
	if amount > l.state.balance {
		return Prepayment{}, ErrOverMaxPrepay
	}
	if amount < l.state.payment && amount != l.state.balance {
		return Prepayment{}, ErrSmallPrepay
	}

	feeBasisPoints := int64(300)
	switch {
	case l.state.paidPeriods >= 36:
		feeBasisPoints = 0
	case l.state.paidPeriods >= 12:
		feeBasisPoints = 100
	}
	fee := amount * feeBasisPoints / 10_000

	l.state.balance -= amount
	return Prepayment{Amount: amount, Fee: fee, Balance: l.state.balance}, nil
}

func (l *Loan) SetRate(monthlyRate int64, effectivePeriod int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !inRange(monthlyRate, 1, 1_000_000) || !inRange(effectivePeriod, 1, 1_000) {
		return ErrInvalidArgument
	}
	if l.state.balance == 0 {
		return ErrClosedLoan
	}
	if effectivePeriod <= l.state.paidPeriods {
		return ErrRateExpired
	}

	candidate := cloneState(l.state)
	candidate.rates[effectivePeriod] = monthlyRate
	if err := validateSchedule(candidate); err != nil {
		return err
	}

	l.state.rates = candidate.rates
	return nil
}

func (l *Loan) Holiday(count int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !inRange(count, 1, 12) {
		return ErrInvalidArgument
	}
	if l.state.balance == 0 {
		return ErrClosedLoan
	}
	if l.state.holidayLeft > 0 {
		return ErrHolidayExists
	}

	candidate := cloneState(l.state)
	candidate.holidayLeft = count
	if err := validateSchedule(candidate); err != nil {
		return err
	}

	l.state.holidayLeft = count
	return nil
}

func (l *Loan) Remaining() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	simulation := cloneState(l.state)
	var periods int64
	for simulation.balance > 0 {
		period := simulation.paidPeriods + 1
		interest := interestFor(simulation, period)
		if simulation.holidayLeft > 0 {
			simulation.holidayLeft--
		} else if simulation.balance+interest <= simulation.payment {
			simulation.balance = 0
		} else {
			simulation.balance -= simulation.payment - interest
		}
		simulation.paidPeriods++
		periods++
	}
	return periods
}

func validateSchedule(initial loanState) error {
	simulation := cloneState(initial)
	for periodNumber := int64(1); periodNumber <= 601; periodNumber++ {
		period := simulation.paidPeriods + 1
		interest := interestFor(simulation, period)

		if simulation.holidayLeft > 0 {
			simulation.holidayLeft--
		} else if simulation.balance+interest <= simulation.payment {
			if periodNumber == 601 {
				return ErrTermExceeded
			}
			return nil
		} else if simulation.payment <= interest {
			return ErrNotAmortizable
		} else {
			simulation.balance -= simulation.payment - interest
		}

		simulation.paidPeriods++
	}
	return ErrTermExceeded
}

func interestFor(state loanState, period int64) int64 {
	rate := rateAt(state, period)
	return (state.balance*rate + 500_000) / 1_000_000
}

func rateAt(state loanState, period int64) int64 {
	rate := state.initialRate
	latest := int64(0)
	for effectivePeriod, candidateRate := range state.rates {
		if effectivePeriod <= period && effectivePeriod >= latest {
			latest = effectivePeriod
			rate = candidateRate
		}
	}
	return rate
}

func cloneState(source loanState) loanState {
	clone := source
	clone.rates = make(map[int64]int64, len(source.rates))
	for period, rate := range source.rates {
		clone.rates[period] = rate
	}
	return clone
}

func inRange(value int64, minimum int64, maximum int64) bool {
	return value >= minimum && value <= maximum
}

func IsLoanError(err error, code ErrorCode) bool {
	var loanErr LoanError
	return errors.As(err, &loanErr) && loanErr.Code == code
}
