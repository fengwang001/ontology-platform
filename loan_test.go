package loan

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, principal int64, monthlyRate int64, payment int64) *Loan {
	t.Helper()
	l, err := New(principal, monthlyRate, payment)
	if err != nil {
		t.Fatalf("New(%d, %d, %d): %v", principal, monthlyRate, payment, err)
	}
	return l
}

func mustPay(t *testing.T, l *Loan) Payment {
	t.Helper()
	payment, err := l.Pay()
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	return payment
}

func payN(t *testing.T, l *Loan, count int) []Payment {
	t.Helper()
	payments := make([]Payment, 0, count)
	for range count {
		payments = append(payments, mustPay(t, l))
	}
	return payments
}

func assertPayment(t *testing.T, got Payment, want Payment) {
	t.Helper()
	if got != want {
		t.Fatalf("payment = %+v, want %+v", got, want)
	}
}

func loanAfterPeriods(t *testing.T, principal int64, rate int64, payment int64, periods int) *Loan {
	t.Helper()
	l := mustNew(t, principal, rate, payment)
	payN(t, l, periods)
	return l
}

func TestExampleOneStandardPath(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 30_000)

	assertPayment(t, mustPay(t, l), Payment{1, 30_000, 1_000, 29_000, 71_000})
	if got := l.Remaining(); got != 3 {
		t.Fatalf("Remaining = %d, want 3", got)
	}

	assertPayment(t, mustPay(t, l), Payment{2, 30_000, 710, 29_290, 41_710})
	assertPayment(t, mustPay(t, l), Payment{3, 30_000, 417, 29_583, 12_127})
	assertPayment(t, mustPay(t, l), Payment{4, 12_248, 121, 12_127, 0})
	if _, err := l.Pay(); !errors.Is(err, ErrSettled) {
		t.Fatalf("Pay after settlement error = %v, want ErrSettled", err)
	}
}

func TestExampleOnePrepaymentPath(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 30_000)
	mustPay(t, l)

	prepayment, err := l.Prepay(30_000)
	if err != nil {
		t.Fatalf("Prepay: %v", err)
	}
	if prepayment != (Prepayment{Amount: 30_000, Fee: 900, Balance: 41_000}) {
		t.Fatalf("prepayment = %+v", prepayment)
	}
	if got := l.Remaining(); got != 2 {
		t.Fatalf("Remaining = %d, want 2", got)
	}

	assertPayment(t, mustPay(t, l), Payment{2, 30_000, 410, 29_590, 11_410})
	assertPayment(t, mustPay(t, l), Payment{3, 11_524, 114, 11_410, 0})
}

func TestExampleTwoHolidayAndRateChange(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 30_000)
	mustPay(t, l)

	if err := l.SetRate(20_000, 3); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if err := l.Holiday(2); err != nil {
		t.Fatalf("Holiday: %v", err)
	}
	if got := l.Remaining(); got != 5 {
		t.Fatalf("Remaining after holiday = %d, want 5", got)
	}

	assertPayment(t, mustPay(t, l), Payment{2, 710, 710, 0, 71_000})
	assertPayment(t, mustPay(t, l), Payment{3, 1_420, 1_420, 0, 71_000})
	assertPayment(t, mustPay(t, l), Payment{4, 30_000, 1_420, 28_580, 42_420})
	assertPayment(t, mustPay(t, l), Payment{5, 30_000, 848, 29_152, 13_268})
	assertPayment(t, mustPay(t, l), Payment{6, 13_533, 265, 13_268, 0})
}

func TestInterestRoundingBoundaries(t *testing.T) {
	l := mustNew(t, 1_000_000, 500, 10_000)
	payment := mustPay(t, l)
	if payment.Interest != 500 {
		t.Fatalf("half-round interest = %d, want 500", payment.Interest)
	}

	l = mustNew(t, 2, 250_000, 10_000)
	if payment := mustPay(t, l); payment.Interest != 1 {
		t.Fatalf("interest = %d, want 1", payment.Interest)
	}
	if got := interestFor(1, 499_999); got != 0 {
		t.Fatalf("one-below-half interest = %d, want 0", got)
	}
}

func TestFinalPaymentBoundary(t *testing.T) {
	l := mustNew(t, 9_901, 10_000, 10_000)
	payment := mustPay(t, l)
	if payment != (Payment{1, 10_000, 99, 9_901, 0}) {
		t.Fatalf("payment = %+v, want final payment", payment)
	}

	l = mustNew(t, 9_902, 10_000, 10_000)
	payment = mustPay(t, l)
	if payment != (Payment{1, 10_000, 99, 9_901, 1}) {
		t.Fatalf("payment = %+v, want ordinary payment", payment)
	}
}

func TestHolidayAllowsInterestOnlyThenRejectsFutureUnamortizablePlan(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 10_000)
	mustPay(t, l)
	if err := l.Holiday(1); err != nil {
		t.Fatalf("Holiday: %v", err)
	}
	if err := l.SetRate(10_000, 3); err != nil {
		t.Fatalf("SetRate resume rate: %v", err)
	}
	if err := l.SetRate(500_000, 2); err != nil {
		t.Fatalf("SetRate holiday-only high rate: %v", err)
	}
	assertPayment(t, mustPay(t, l), Payment{2, 45_500, 45_500, 0, 91_000})

	if err := l.SetRate(500_000, 3); !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate after holiday error = %v, want ErrNotAmortizable", err)
	}
}

func TestPrepaymentBoundaries(t *testing.T) {
	l := loanAfterPeriods(t, 100_000, 10_000, 30_000, 1)
	if _, err := l.Prepay(30_000); err != nil {
		t.Fatalf("Prepay(30000): %v", err)
	}

	l = loanAfterPeriods(t, 100_000, 10_000, 30_000, 1)
	_, err := l.Prepay(29_999)
	if !errors.Is(err, ErrBelowMinimum) {
		t.Fatalf("Prepay(29999) error = %v, want ErrBelowMinimum", err)
	}

	l = loanAfterPeriods(t, 100_000, 10_000, 30_000, 3)
	prepayment, err := l.Prepay(12_127)
	if err != nil {
		t.Fatalf("Prepay(final balance): %v", err)
	}
	if prepayment.Balance != 0 {
		t.Fatalf("balance = %d, want 0", prepayment.Balance)
	}

	l = loanAfterPeriods(t, 100_000, 10_000, 30_000, 1)
	_, err = l.Prepay(71_001)
	if !errors.Is(err, ErrOverpayment) {
		t.Fatalf("Prepay(balance+1) error = %v, want ErrOverpayment", err)
	}
}

func TestPrepaymentFeeBoundariesAndFloor(t *testing.T) {
	for _, tc := range []struct {
		periods int
	}{
		{11},
		{12},
		{35},
		{36},
	} {
		t.Run(fmt.Sprintf("periods_%d", tc.periods), func(t *testing.T) {
			l := loanAfterPeriods(t, 500_000_000_000, 1, 10_000_000_000, tc.periods)
			prepayment, err := l.Prepay(10_000_000_000)
			if err != nil {
				t.Fatalf("Prepay: %v", err)
			}
			wantFee := prepaymentFee(10_000_000_000, int64(tc.periods))
			if prepayment.Fee != wantFee {
				t.Fatalf("fee = %d, want %d", prepayment.Fee, wantFee)
			}
		})
	}

	l := loanAfterPeriods(t, 500_000_000_000, 1, 30_000_000_000, 1)
	prepayment, err := l.Prepay(30_000_000_001)
	if err != nil {
		t.Fatalf("Prepay: %v", err)
	}
	wantFee := int64(900_000_000)
	if prepayment.Fee != wantFee {
		t.Fatalf("fee = %d, want floored %d", prepayment.Fee, wantFee)
	}
}

func TestSetRateBoundariesAndOverride(t *testing.T) {
	l := loanAfterPeriods(t, 100_000, 10_000, 30_000, 1)
	err := l.SetRate(20_000, 1)
	if !errors.Is(err, ErrRatePeriodPassed) {
		t.Fatalf("SetRate(k0=k) error = %v, want ErrRatePeriodPassed", err)
	}
	if err := l.SetRate(20_000, 2); err != nil {
		t.Fatalf("SetRate(k0=k+1): %v", err)
	}

	if err := l.SetRate(5_000, 2); err != nil {
		t.Fatalf("override SetRate: %v", err)
	}
	if got := l.rateChanges[2]; got != 5_000 {
		t.Fatalf("rate for period 2 = %d, want 5000", got)
	}

	l = mustNew(t, 20, 1, 10)
	err = l.SetRate(1_000_000, 2)
	if !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate equal interest error = %v, want ErrNotAmortizable", err)
	}
	if err := l.SetRate(900_000, 2); err != nil {
		t.Fatalf("SetRate interest one below payment: %v", err)
	}
}

func TestRateChangeAppliesDuringHoliday(t *testing.T) {
	l := mustNew(t, 71_000, 10_000, 30_000)
	if err := l.SetRate(20_000, 1); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if err := l.Holiday(1); err != nil {
		t.Fatalf("Holiday: %v", err)
	}
	assertPayment(t, mustPay(t, l), Payment{1, 1_420, 1_420, 0, 71_000})
}

func TestHolidayTermLimitAndRejection(t *testing.T) {
	l := mustNew(t, 599, 1, 1)
	if err := l.Holiday(1); err != nil {
		t.Fatalf("Holiday resulting in 600 periods: %v", err)
	}
	if got := l.Remaining(); got != 600 {
		t.Fatalf("Remaining = %d, want 600", got)
	}

	l = mustNew(t, 600, 1, 1)
	err := l.Holiday(1)
	if !errors.Is(err, ErrTermExceeded) {
		t.Fatalf("Holiday resulting in 601 periods error = %v, want ErrTermExceeded", err)
	}

	l = mustNew(t, 599, 1, 1)
	if err := l.Holiday(1); err != nil {
		t.Fatalf("first Holiday: %v", err)
	}
	if err := l.Holiday(1); !errors.Is(err, ErrHolidayActive) {
		t.Fatalf("second Holiday error = %v, want ErrHolidayActive", err)
	}
}

func TestValidationErrorPriorities(t *testing.T) {
	_, err := New(0, 10_000_000, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New invalid priority error = %v, want ErrInvalidArgument", err)
	}

	_, err = New(10_000, 10_000, 5)
	if !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("New unamortizable error = %v, want ErrNotAmortizable", err)
	}

	_, err = New(601, 1, 1)
	if !errors.Is(err, ErrTermExceeded) {
		t.Fatalf("New term error = %v, want ErrTermExceeded", err)
	}

	l := mustNew(t, 100_000, 10_000, 30_000)
	if err := l.Holiday(1); err != nil {
		t.Fatalf("Holiday: %v", err)
	}
	err = l.SetRate(500_000, 2)
	if !errors.Is(err, ErrNotAmortizable) {
		t.Fatalf("SetRate unamortizable before term error = %v, want ErrNotAmortizable", err)
	}

	l = mustNew(t, 600, 1, 1)
	err = l.Holiday(1)
	if !errors.Is(err, ErrTermExceeded) {
		t.Fatalf("Holiday term error = %v, want ErrTermExceeded", err)
	}
}

func TestSettledAndRejectedOperationsDoNotMutate(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 30_000)
	mustPay(t, l)
	balanceBefore := l.Balance()
	periodsBefore := l.PaidPeriods()
	ratesBefore := cloneRates(l.rateChanges)
	holidayBefore := l.h

	invalidCalls := []func() error{
		func() error { _, err := l.Prepay(0); return err },
		func() error { return l.SetRate(0, 3) },
		func() error { return l.Holiday(0) },
	}
	for index, call := range invalidCalls {
		if err := call(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid call %d error = %v, want ErrInvalidArgument", index, err)
		}
	}

	mustPay(t, l)
	mustPay(t, l)
	mustPay(t, l)

	for _, call := range []func() error{
		func() error { _, err := l.Pay(); return err },
		func() error { _, err := l.Prepay(1); return err },
		func() error { return l.SetRate(10_000, 5) },
		func() error { return l.Holiday(1) },
	} {
		if err := call(); !errors.Is(err, ErrSettled) {
			t.Fatalf("settled call error = %v, want ErrSettled", err)
		}
	}

	if l.Balance() != 0 || l.PaidPeriods() != 4 {
		t.Fatalf("settled state = bal:%d k:%d", l.Balance(), l.PaidPeriods())
	}
	if balanceBefore != 71_000 || periodsBefore != 1 || holidayBefore != 0 {
		t.Fatalf("captured pre-rejection state changed")
	}
	if len(l.rateChanges) != len(ratesBefore) {
		t.Fatalf("rate changes mutated by rejected calls")
	}
}

func TestPrepaymentDoesNotIncreaseRemainingAndBalanceMonotonic(t *testing.T) {
	l := mustNew(t, 100_000, 10_000, 30_000)
	mustPay(t, l)
	before := l.Remaining()
	balance := l.Balance()

	prepayment, err := l.Prepay(30_000)
	if err != nil {
		t.Fatalf("Prepay: %v", err)
	}
	if prepayment.Balance >= balance {
		t.Fatalf("balance did not decrease: before %d after %d", balance, prepayment.Balance)
	}
	if l.Remaining() > before {
		t.Fatalf("Remaining increased: before %d after %d", before, l.Remaining())
	}

	for l.Balance() > 0 {
		payment := mustPay(t, l)
		if payment.Principal < 0 || payment.Balance < 0 {
			t.Fatalf("invalid payment %+v", payment)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	l := mustNew(t, 500_000_000_000, 1, 10_000_000_000)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 20 {
				_ = l.Remaining()
				_ = l.Balance()
				_ = l.PaidPeriods()
				if id%4 == 0 {
					_, _ = l.Pay()
				} else if id%4 == 1 {
					_, _ = l.Prepay(10_000_000_000)
				} else if id%4 == 2 {
					_ = l.SetRate(1, l.PaidPeriods()+1)
				} else {
					_ = l.Holiday(1)
				}
			}
		}(worker)
	}
	wg.Wait()

	if l.Balance() < 0 || l.PaidPeriods() < 0 || l.Remaining() > maxPeriods {
		t.Fatalf("invalid concurrent final state: bal=%d k=%d remaining=%d", l.Balance(), l.PaidPeriods(), l.Remaining())
	}
}
