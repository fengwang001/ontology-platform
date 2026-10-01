package loan

import (
	"errors"
	"testing"
)

func TestInterestRoundingBoundaries(t *testing.T) {
	halfUp, err := New(500_000, 1, 30_000)
	if err != nil {
		t.Fatalf("New(half-up) error = %v", err)
	}
	payment, err := halfUp.Pay()
	if err != nil {
		t.Fatalf("Pay(half-up) error = %v", err)
	}
	if payment.Interest != 1 {
		t.Fatalf("half-up interest = %d, want 1", payment.Interest)
	}

	roundDown, err := New(499_999, 1, 300_000)
	if err != nil {
		t.Fatalf("New(round-down) error = %v", err)
	}
	payment, err = roundDown.Pay()
	if err != nil {
		t.Fatalf("Pay(round-down) error = %v", err)
	}
	if payment.Interest != 0 {
		t.Fatalf("round-down interest = %d, want 0", payment.Interest)
	}
}

func TestFinalPaymentBoundary(t *testing.T) {
	exact, err := New(29_703, 10_000, 30_000)
	if err != nil {
		t.Fatalf("New(exact final) error = %v", err)
	}
	assertPay(t, exact, Payment{Period: 1, Amount: 30_000, Interest: 297, Principal: 29_703, Balance: 0})

	oneMore, err := New(29_704, 10_000, 30_000)
	if err != nil {
		t.Fatalf("New(one over final) error = %v", err)
	}
	assertPay(t, oneMore, Payment{Period: 1, Amount: 30_000, Interest: 297, Principal: 29_703, Balance: 1})
}

func TestHolidayAmortizationBoundary(t *testing.T) {
	withoutHoliday := startedLoan(t, 100_000, 10_000, 30_000)
	if err := withoutHoliday.SetRate(500_000, 2); !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate() after holiday error = %v, want %v", err, ErrNotAmortizable)
	}

	withHoliday := startedLoan(t, 100_000, 10_000, 30_000)
	if err := withHoliday.Holiday(1); err != nil {
		t.Fatalf("Holiday() before temporary high rate error = %v", err)
	}
	if err := withHoliday.SetRate(500_000, 2); !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate(high after holiday without reset) error = %v, want %v", err, ErrNotAmortizable)
	}
	if err := withHoliday.SetRate(10_000, 3); err != nil {
		t.Fatalf("SetRate(reset after holiday) error = %v", err)
	}
	if err := withHoliday.SetRate(500_000, 2); err != nil {
		t.Fatalf("SetRate(high during holiday) error = %v", err)
	}
	assertPay(t, withHoliday, Payment{Period: 2, Amount: 35_500, Interest: 35_500, Balance: 71_000})
	assertPay(t, withHoliday, Payment{Period: 3, Amount: 30_000, Interest: 710, Principal: 29_290, Balance: 41_710})
}

func TestPrepaymentBoundaries(t *testing.T) {
	equalToPayment := startedLoan(t, 100_000, 10_000, 30_000)
	before := snapshot(t, equalToPayment)
	if _, err := equalToPayment.Prepay(29_999); !errors.Is(err, ErrSmallPrepay) {
		t.Fatalf("Prepay(A-1) error = %v, want %v", err, ErrSmallPrepay)
	}
	assertUnchanged(t, equalToPayment, before, "Prepay(A-1)")
	if _, err := equalToPayment.Prepay(30_000); err != nil {
		t.Fatalf("Prepay(A) error = %v", err)
	}

	balanceBelowPayment, err := New(50_000, 10_000, 30_000)
	if err != nil {
		t.Fatalf("New(balance below payment) error = %v", err)
	}
	if _, err := balanceBelowPayment.Pay(); err != nil {
		t.Fatalf("Pay() before small-balance prepayment error = %v", err)
	}
	if _, err := balanceBelowPayment.Prepay(20_499); !errors.Is(err, ErrSmallPrepay) {
		t.Fatalf("Prepay(balance-1 below A) error = %v, want %v", err, ErrSmallPrepay)
	}
	if result, err := balanceBelowPayment.Prepay(20_500); err != nil || result.Balance != 0 {
		t.Fatalf("Prepay(balance below A) = %+v, %v; want payoff", result, err)
	}

	overBalance := startedLoan(t, 100_000, 10_000, 30_000)
	if _, err := overBalance.Prepay(71_001); !errors.Is(err, ErrOverMaxPrepay) {
		t.Fatalf("Prepay(balance+1) error = %v, want %v", err, ErrOverMaxPrepay)
	}
	if _, err := overBalance.Prepay(71_000); err != nil {
		t.Fatalf("Prepay(balance) error = %v", err)
	}
}

func TestPrepaymentFees(t *testing.T) {
	tests := []struct {
		name string
		paid int64
		want int64
	}{
		{name: "k=11 uses 300 basis points", paid: 11, want: 30_000},
		{name: "k=12 uses 100 basis points", paid: 12, want: 10_000},
		{name: "k=35 uses 100 basis points", paid: 35, want: 10_000},
		{name: "k=36 uses 0 basis points", paid: 36, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := loanAfterPeriods(t, 50_000_000, 5_000, 1_000_000, tt.paid)
			result, err := l.Prepay(1_000_000)
			if err != nil {
				t.Fatalf("Prepay() error = %v", err)
			}
			if result.Fee != tt.want {
				t.Fatalf("fee = %d, want %d", result.Fee, tt.want)
			}
		})
	}

	floor, err := New(100_000, 10_000, 33_333)
	if err != nil {
		t.Fatalf("New(fee floor) error = %v", err)
	}
	if _, err := floor.Pay(); err != nil {
		t.Fatalf("Pay() before fee-floor prepayment error = %v", err)
	}
	result, err := floor.Prepay(33_333)
	if err != nil {
		t.Fatalf("Prepay(fee floor) error = %v", err)
	}
	if result.Fee != 999 {
		t.Fatalf("floored fee = %d, want 999", result.Fee)
	}
}

func TestSetRateBoundaries(t *testing.T) {
	l := startedLoan(t, 100_000, 10_000, 30_000)
	before := snapshot(t, l)
	if err := l.SetRate(20_000, 1); !errors.Is(err, ErrRateExpired) {
		t.Fatalf("SetRate(k0=k) error = %v, want %v", err, ErrRateExpired)
	}
	assertUnchanged(t, l, before, "SetRate(k0=k)")
	if err := l.SetRate(20_000, 2); err != nil {
		t.Fatalf("SetRate(k0=k+1) error = %v", err)
	}
	if err := l.SetRate(10_000, 2); err != nil {
		t.Fatalf("SetRate(overwrite) error = %v", err)
	}
	assertPay(t, l, Payment{Period: 2, Amount: 30_000, Interest: 710, Principal: 29_290, Balance: 41_710})

	equalInterest := startedLoan(t, 100_000, 10_000, 30_000)
	if err := equalInterest.SetRate(422_535, 2); !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate(interest=A) error = %v, want %v", err, ErrNotAmortizable)
	}
	if err := equalInterest.SetRate(422_521, 2); err != nil {
		t.Fatalf("SetRate(interest=A-1) error = %v", err)
	}
	payment, err := equalInterest.Pay()
	if err != nil {
		t.Fatalf("Pay() after boundary rate error = %v", err)
	}
	if payment.Interest != 29_999 || payment.Principal != 1 {
		t.Fatalf("boundary payment = %+v, want interest 29999 and principal 1", payment)
	}
}

func TestHolidayTermBoundary(t *testing.T) {
	exact, err := New(30_000*598, 1, 30_000)
	if err != nil {
		t.Fatalf("New(600 with holiday) error = %v", err)
	}
	if err := exact.Holiday(1); err != nil {
		t.Fatalf("Holiday() for 600-period schedule error = %v", err)
	}
	if remaining := exact.Remaining(); remaining != 600 {
		t.Fatalf("Remaining() = %d, want 600", remaining)
	}

	tooLong, err := New(30_000*599, 1, 30_000)
	if err != nil {
		t.Fatalf("New(601 with holiday) error = %v", err)
	}
	if err := tooLong.Holiday(1); !errors.Is(err, ErrTermExceeded) {
		t.Fatalf("Holiday() for 601-period schedule error = %v, want %v", err, ErrTermExceeded)
	}
	if tooLong.state.holidayLeft != 0 {
		t.Fatalf("rejected Holiday() left h = %d, want 0", tooLong.state.holidayLeft)
	}
}

func TestRejectedOperations(t *testing.T) {
	l := startedLoan(t, 100_000, 10_000, 30_000)
	if err := l.Holiday(1); err != nil {
		t.Fatalf("first Holiday() error = %v", err)
	}
	before := snapshot(t, l)
	if err := l.Holiday(1); !errors.Is(err, ErrHolidayExists) {
		t.Fatalf("second Holiday() error = %v, want %v", err, ErrHolidayExists)
	}
	assertUnchanged(t, l, before, "second Holiday()")

	if _, err := New(1_000_000_000_001, 1_000_001, 1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(out of range) error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := New(1_000_000_000_000, 1_000_000, 1); !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("New(unaffordable and over term) error = %v, want %v", err, ErrNotAmortizable)
	}
}

func TestClosedLoanRejections(t *testing.T) {
	l, err := New(1_000, 1, 2_000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	payment, err := l.Pay()
	if err != nil || payment.Balance != 0 {
		t.Fatalf("Pay() = %+v, %v; want payoff", payment, err)
	}
	if _, err := l.Pay(); !errors.Is(err, ErrClosedLoan) {
		t.Fatalf("Pay() after close error = %v, want %v", err, ErrClosedLoan)
	}
	if _, err := l.Prepay(1); !errors.Is(err, ErrClosedLoan) {
		t.Fatalf("Prepay() after close error = %v, want %v", err, ErrClosedLoan)
	}
	if err := l.SetRate(1, 2); !errors.Is(err, ErrClosedLoan) {
		t.Fatalf("SetRate() after close error = %v, want %v", err, ErrClosedLoan)
	}
	if err := l.Holiday(1); !errors.Is(err, ErrClosedLoan) {
		t.Fatalf("Holiday() after close error = %v, want %v", err, ErrClosedLoan)
	}
}

type stateSnapshot struct {
	balance     int64
	paidPeriods int64
	holidayLeft int64
	rates       map[int64]int64
}

func startedLoan(t *testing.T, principal int64, rate int64, payment int64) *Loan {
	t.Helper()
	l, err := New(principal, rate, payment)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) error = %v", principal, rate, payment, err)
	}
	if _, err := l.Pay(); err != nil {
		t.Fatalf("initial Pay() error = %v", err)
	}
	return l
}

func loanAfterPeriods(t *testing.T, principal int64, rate int64, payment int64, periods int64) *Loan {
	t.Helper()
	l, err := New(principal, rate, payment)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) error = %v", principal, rate, payment, err)
	}
	for index := int64(0); index < periods; index++ {
		if _, err := l.Pay(); err != nil {
			t.Fatalf("Pay() period %d error = %v", index+1, err)
		}
	}
	return l
}

func snapshot(t *testing.T, l *Loan) stateSnapshot {
	t.Helper()
	l.mu.RLock()
	defer l.mu.RUnlock()
	rates := make(map[int64]int64, len(l.state.rates))
	for period, rate := range l.state.rates {
		rates[period] = rate
	}
	return stateSnapshot{
		balance:     l.state.balance,
		paidPeriods: l.state.paidPeriods,
		holidayLeft: l.state.holidayLeft,
		rates:       rates,
	}
}

func assertUnchanged(t *testing.T, l *Loan, before stateSnapshot, operation string) {
	t.Helper()
	after := snapshot(t, l)
	if after.balance != before.balance ||
		after.paidPeriods != before.paidPeriods ||
		after.holidayLeft != before.holidayLeft ||
		len(after.rates) != len(before.rates) {
		t.Fatalf("%s changed state: before %+v, after %+v", operation, before, after)
	}
	for period, rate := range before.rates {
		if after.rates[period] != rate {
			t.Fatalf("%s changed rate at period %d: before %d, after %d", operation, period, rate, after.rates[period])
		}
	}
}
