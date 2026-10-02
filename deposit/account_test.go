package deposit

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, start int64) *Account {
	t.Helper()
	a, err := New(start)
	if err != nil {
		t.Fatalf("New(%d): %v", start, err)
	}
	return a
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

// Remainder carries over across settlements (the spec example).
func TestSpecExampleRemainderCarryOver(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	i, bal, rem, err := a.Settle(10)
	mustOK(t, err)
	if i != 1013 || bal != 1001013 || rem != 3200000 {
		t.Fatalf("Settle(10) = (%d, %d, %d), want (1013, 1001013, 3200000)", i, bal, rem)
	}
	// The credited interest 1013 joins the products from day 10 on.
	i, bal, rem, err = a.Settle(20)
	mustOK(t, err)
	if i != 1015 || bal != 1002028 || rem != 2897450 {
		t.Fatalf("Settle(20) = (%d, %d, %d), want (1015, 1002028, 2897450)", i, bal, rem)
	}
}

// N exactly divisible by D leaves R == 0.
func TestExactDivisionRemZero(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 10000))
	mustOK(t, a.SetRate(0, 3600))
	i, bal, rem, err := a.Settle(1)
	mustOK(t, err)
	if i != 10 || bal != 10010 || rem != 0 {
		t.Fatalf("Settle(1) = (%d, %d, %d), want (10, 10010, 0)", i, bal, rem)
	}
}

// Coverage of exactly 3660 days passes, 3661 is rejected as invalid.
func TestSettleCoverageLimit(t *testing.T) {
	a := mustNew(t, 0)
	_, _, _, err := a.Settle(3660)
	mustOK(t, err)
	_, _, _, err = a.Settle(3660 + 3661)
	mustErr(t, err, ErrInvalidParam)
	// Rejection must not move prev: 3660 more days still fits.
	_, _, _, err = a.Settle(7320)
	mustOK(t, err)
}

// A rate change applies from its own day on; the day before keeps the old rate.
func TestRateChangeMidInterval(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	mustOK(t, a.SetRate(5, 730))
	i, bal, rem, err := a.Settle(10)
	mustOK(t, err)
	// N = 5*1000000*365 + 5*1000000*730 = 5475000000.
	if i != 1520 || bal != 1001520 || rem != 3000000 {
		t.Fatalf("Settle(10) = (%d, %d, %d), want (1520, 1001520, 3000000)", i, bal, rem)
	}
}

// Multiple same-day flows count only through the day-end balance.
func TestSameDayMultipleFlows(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 100))
	mustOK(t, a.Deposit(0, 200))
	mustOK(t, a.Withdraw(0, 50))
	mustOK(t, a.SetRate(0, 3600))
	i, _, rem, err := a.Settle(1)
	mustOK(t, err)
	// Day-end balance of day 0 is 250: N = 250*3600 = 900000.
	if i != 0 || rem != 900000 {
		t.Fatalf("Settle(1) = (i=%d, R=%d), want (0, 900000)", i, rem)
	}
	i, _, rem, err = a.Settle(2)
	mustOK(t, err)
	if i != 0 || rem != 1800000 {
		t.Fatalf("Settle(2) = (i=%d, R=%d), want (0, 1800000)", i, rem)
	}
	// Days 2 and 3 add 2*900000; N = 3600000 exactly divides D.
	i, bal, rem, err := a.Settle(4)
	mustOK(t, err)
	if i != 1 || bal != 251 || rem != 0 {
		t.Fatalf("Settle(4) = (%d, %d, %d), want (1, 251, 0)", i, bal, rem)
	}
}

// A deposit on the settle day is excluded from this settlement and counted
// in the next one.
func TestSettleDayDepositExcluded(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	i, _, _, err := a.Settle(10)
	mustOK(t, err)
	if i != 1013 {
		t.Fatalf("Settle(10) interest = %d, want 1013", i)
	}
	mustOK(t, a.Deposit(10, 500000))
	i, bal, rem, err := a.Settle(20)
	mustOK(t, err)
	// Days 10..19: B = 1001013 + 500000 = 1501013.
	// N = 3200000 + 10*1501013*365 = 5481897450.
	if i != 1522 || bal != 1502535 || rem != 2697450 {
		t.Fatalf("Settle(20) = (%d, %d, %d), want (1522, 1502535, 2697450)", i, bal, rem)
	}
}

// Credited interest participates in the products from the settle day on.
func TestCreditedInterestJoinsNextInterval(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 3600000))
	mustOK(t, a.SetRate(0, 3600))
	i, bal, _, err := a.Settle(1)
	mustOK(t, err)
	if i != 3600 || bal != 3603600 {
		t.Fatalf("Settle(1) = (i=%d, bal=%d), want (3600, 3603600)", i, bal)
	}
	// Day 1 balance is 3603600 (includes the 3600 credited interest).
	i, bal, rem, err := a.Settle(2)
	mustOK(t, err)
	// N = 3603600*3600 = 12972960000.
	if i != 3603 || bal != 3607203 || rem != 2160000 {
		t.Fatalf("Settle(2) = (%d, %d, %d), want (3603, 3607203, 2160000)", i, bal, rem)
	}
}

func TestWithdrawToZero(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 500))
	mustOK(t, a.Withdraw(0, 500))
	if a.Balance() != 0 {
		t.Fatalf("balance = %d, want 0", a.Balance())
	}
	mustErr(t, a.Withdraw(0, 1), ErrInsufficientFunds)
}

// Invalid parameters, out-of-order dates, empty ranges and unsettled
// intervals are distinguishable.
func TestErrorDistinction(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New(-1) = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1000001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New(1000001) = %v, want ErrInvalidParam", err)
	}

	a := mustNew(t, 3)
	mustErr(t, a.Deposit(0, 100), ErrOutOfOrder) // d < m == start
	mustErr(t, a.Deposit(3, 0), ErrInvalidParam)
	mustErr(t, a.Deposit(3, 1000000001), ErrInvalidParam)
	mustErr(t, a.Deposit(-1, 5), ErrInvalidParam)
	mustErr(t, a.Deposit(10000001, 5), ErrInvalidParam)
	mustErr(t, a.SetRate(3, 10001), ErrInvalidParam)
	mustErr(t, a.SetRate(3, -1), ErrInvalidParam)
	mustErr(t, a.Withdraw(3, 0), ErrInvalidParam)

	b := mustNew(t, 0)
	_, _, _, err := b.Settle(0)
	mustErr(t, err, ErrEmptyRange) // d <= prev
	mustOK(t, b.Deposit(5, 10))
	_, _, _, err = b.Settle(3)
	mustErr(t, err, ErrOutOfOrder) // d < m, reported before empty range
	_, _, _, err = b.Settle(5)
	mustOK(t, err)
	_, _, _, err = b.Settle(5)
	mustErr(t, err, ErrEmptyRange) // d == prev
	_, _, _, err = b.Settle(10000001)
	mustErr(t, err, ErrInvalidParam)

	// Correct: invalid parameters vs unsettled interval.
	mustErr3 := func(d, delta int64, want error) {
		t.Helper()
		if _, _, _, err := b.Correct(d, delta); !errors.Is(err, want) {
			t.Fatalf("Correct(%d, %d) = %v, want %v", d, delta, err, want)
		}
	}
	mustErr3(0, 0, ErrInvalidParam)          // zero delta
	mustErr3(0, 1000000001, ErrInvalidParam) // |delta| too large
	mustErr3(-1, 1, ErrInvalidParam)
	mustErr3(5, 1, ErrNotSettled) // d >= prev

	c := mustNew(t, 5)
	_, _, _, err = c.Settle(10)
	mustOK(t, err)
	if _, _, _, err := c.Correct(3, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Correct below start = %v, want ErrInvalidParam", err)
	}
	if _, _, _, err := c.Correct(10, 1); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("Correct at prev = %v, want ErrNotSettled", err)
	}
	if _, _, _, err := c.Correct(7, 1); err != nil {
		t.Fatalf("Correct(7, 1): %v", err)
	}

	d := mustNew(t, 0)
	mustOK(t, d.Deposit(0, 10))
	mustErr(t, d.Withdraw(0, 11), ErrInsufficientFunds)

	e := mustNew(t, 0)
	mustOK(t, e.Deposit(0, 1000000000))
	mustErr(t, e.Deposit(0, 1), ErrBalanceOverflow)
}

func specExampleAccount(t *testing.T) *Account {
	t.Helper()
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000))
	mustOK(t, a.SetRate(0, 365))
	settleOK(t, a, 10)
	settleOK(t, a, 20)
	return a
}

func settleOK(t *testing.T, a *Account, d int64) {
	t.Helper()
	_, _, _, err := a.Settle(d)
	mustOK(t, err)
}

// The spec Correct example: delta on day 5 recomputes both records, and the
// new interest of the first record joins the products of the second.
func TestCorrectSpecExample(t *testing.T) {
	a := specExampleAccount(t)
	bal, rem, changes, err := a.Correct(5, 1000000)
	mustOK(t, err)
	if bal != 2003550 || rem != 548000 {
		t.Fatalf("Correct = (bal=%d, R=%d), want (2003550, 548000)", bal, rem)
	}
	want := []Change{
		{SettleDay: 10, OldInterest: 1013, NewInterest: 1520, OldRemainder: 3200000, NewRemainder: 3000000},
		{SettleDay: 20, OldInterest: 1015, NewInterest: 2030, OldRemainder: 2897450, NewRemainder: 548000},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	// Conservation: total settled interest * D + R == total products.
	var sumI int64
	for _, rec := range a.records {
		sumI += rec.interest
	}
	if got := sumI*D + a.rem; got != 12780548000 {
		t.Fatalf("sum(i)*D + R = %d, want 12780548000", got)
	}
}

// A correction dated exactly on a record's settle day leaves that record
// untouched; one day earlier rewrites it.
func TestCorrectSettleDayBoundary(t *testing.T) {
	a := specExampleAccount(t)
	bal, rem, changes, err := a.Correct(10, 1000000)
	mustOK(t, err)
	want := []Change{
		{SettleDay: 20, OldInterest: 1015, NewInterest: 2029, OldRemainder: 2897450, NewRemainder: 2497450},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if bal != 2003042 || rem != 2497450 {
		t.Fatalf("Correct = (bal=%d, R=%d), want (2003042, 2497450)", bal, rem)
	}

	b := specExampleAccount(t)
	bal, rem, changes, err = b.Correct(9, 1000000)
	mustOK(t, err)
	want = []Change{
		{SettleDay: 10, OldInterest: 1013, NewInterest: 1115, OldRemainder: 3200000, NewRemainder: 1000000},
		{SettleDay: 20, OldInterest: 1015, NewInterest: 2029, OldRemainder: 2897450, NewRemainder: 669750},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if bal != 2003144 || rem != 669750 {
		t.Fatalf("Correct = (bal=%d, R=%d), want (2003144, 669750)", bal, rem)
	}
}

// A negative delta bringing a day-end balance to exactly 0 passes; one unit
// more is rejected and changes nothing.
func TestCorrectNegativeBalance(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 100))
	settleOK(t, a, 10)
	bal, _, _, err := a.Correct(5, -100)
	mustOK(t, err)
	if bal != 0 {
		t.Fatalf("balance = %d, want 0", bal)
	}
	_, _, _, err = a.Correct(5, -1)
	mustErr(t, err, ErrNegativeBalance)
	if a.Balance() != 0 {
		t.Fatalf("balance after rejected Correct = %d, want 0", a.Balance())
	}
}

// Records with unchanged interest but changed remainder appear in the change
// list; records with both unchanged do not.
func TestCorrectChangeListRules(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000))
	mustOK(t, a.SetRate(0, 365))
	settleOK(t, a, 1) // N = 365000: i = 0, R = 365000
	bal, rem, changes, err := a.Correct(0, 1)
	mustOK(t, err)
	// N = 1001*365 = 365365: same interest 0, remainder 365000 -> 365365.
	want := []Change{
		{SettleDay: 1, OldInterest: 0, NewInterest: 0, OldRemainder: 365000, NewRemainder: 365365},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if bal != 1001 || rem != 365365 {
		t.Fatalf("Correct = (bal=%d, R=%d), want (1001, 365365)", bal, rem)
	}

	// Rate 0: the recomputed record is identical and must not be listed.
	b := mustNew(t, 0)
	mustOK(t, b.Deposit(0, 1000))
	settleOK(t, b, 1)
	bal, rem, changes, err = b.Correct(0, 500)
	mustOK(t, err)
	if len(changes) != 0 {
		t.Fatalf("changes = %+v, want empty", changes)
	}
	if bal != 1500 || rem != 0 {
		t.Fatalf("Correct = (bal=%d, R=%d), want (1500, 0)", bal, rem)
	}
}

// A correction pushing a day-end balance above 1e11 is rejected.
func TestCorrectOverflow(t *testing.T) {
	a := mustNew(t, 0)
	mustOK(t, a.Deposit(0, 1000000000))
	settleOK(t, a, 10)
	for i := 0; i < 99; i++ {
		if _, _, _, err := a.Correct(0, 1000000000); err != nil {
			t.Fatalf("Correct #%d: %v", i, err)
		}
	}
	if a.Balance() != 100000000000 {
		t.Fatalf("balance = %d, want 100000000000", a.Balance())
	}
	_, _, _, err := a.Correct(0, 1000000000)
	mustErr(t, err, ErrCorrectedOverflow)
	if a.Balance() != 100000000000 {
		t.Fatalf("balance after rejected Correct = %d, want 100000000000", a.Balance())
	}
}

type stateSnap struct {
	balance, rem, prev, m int64
	flows                 map[int64]int64
	rates                 map[int64]int64
	rateDays              []int64
	records               []settleRecord
}

func snap(a *Account) stateSnap {
	s := stateSnap{
		balance: a.balance,
		rem:     a.rem,
		prev:    a.prev,
		m:       a.m,
		flows:   make(map[int64]int64, len(a.flows)),
		rates:   make(map[int64]int64, len(a.rates)),
	}
	for k, v := range a.flows {
		s.flows[k] = v
	}
	for k, v := range a.rates {
		s.rates[k] = v
	}
	s.rateDays = append([]int64(nil), a.rateDays...)
	s.records = append([]settleRecord(nil), a.records...)
	return s
}

func checkState(t *testing.T, a *Account, s stateSnap) {
	t.Helper()
	got := snap(a)
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("state changed:\n got %+v\nwant %+v", got, s)
	}
}

// Every rejected operation leaves balance, rate, prev, R, m and all
// settlement records untouched.
func TestRejectedOpsKeepState(t *testing.T) {
	a := mustNew(t, 2)
	mustOK(t, a.Deposit(2, 1000))
	mustOK(t, a.SetRate(2, 365))
	settleOK(t, a, 5)
	mustOK(t, a.Deposit(5, 500))
	settleOK(t, a, 8)
	s := snap(a)

	rejections := []func() error{
		func() error { return a.Deposit(1, 5) },                        // out of order
		func() error { return a.Deposit(8, 0) },                        // invalid amount
		func() error { return a.Deposit(8, 1000000001) },               // invalid amount
		func() error { return a.Withdraw(8, 1000000000) },              // insufficient
		func() error { return a.Withdraw(8, -3) },                      // invalid amount
		func() error { return a.SetRate(7, 5) },                        // out of order
		func() error { return a.SetRate(8, 10001) },                    // invalid rate
		func() error { _, _, _, e := a.Settle(8); return e },           // empty range
		func() error { _, _, _, e := a.Settle(8 + 3661); return e },    // coverage too long
		func() error { _, _, _, e := a.Settle(7); return e },           // out of order
		func() error { _, _, _, e := a.Correct(8, 5); return e },       // not settled
		func() error { _, _, _, e := a.Correct(1, 5); return e },       // below start
		func() error { _, _, _, e := a.Correct(2, 0); return e },       // zero delta
		func() error { _, _, _, e := a.Correct(2, -100000); return e }, // negative balance
	}
	for i, op := range rejections {
		if err := op(); err == nil {
			t.Fatalf("rejection #%d unexpectedly succeeded", i)
		}
		checkState(t, a, s)
	}
}

// Concurrent operations and queries behave as some serial order.
func TestConcurrentUse(t *testing.T) {
	a := mustNew(t, 0)
	const goroutines = 8
	const perG = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := a.Deposit(0, 1); err != nil {
					t.Error(err)
				}
				_ = a.Balance()
				_ = a.Remainder()
			}
		}()
	}
	wg.Wait()
	if got := a.Balance(); got != goroutines*perG {
		t.Fatalf("balance = %d, want %d", got, goroutines*perG)
	}
	mustOK(t, a.SetRate(0, 365))
	_, bal, rem, err := a.Settle(1)
	mustOK(t, err)
	// N = 800*365 = 292000 < D: no interest, remainder carried.
	if bal != goroutines*perG || rem != 292000 {
		t.Fatalf("Settle(1) = (bal=%d, R=%d), want (%d, 292000)", bal, rem, goroutines*perG)
	}
	if rem < 0 || rem >= D {
		t.Fatalf("R = %d out of [0, %d)", rem, D)
	}
}
