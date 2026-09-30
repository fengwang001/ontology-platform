package quota

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"sync"
	"testing"
)

// newLogged constructs a ledger whose input/output/decision log is attached to
// the test output, so `go test -v` shows the basis for each assertion.
func newLogged(t *testing.T, t0, p, q, m int64) *Ledger {
	t.Helper()
	l, err := New(t0, p, q, m)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d) unexpected error: %v", t0, p, q, m, err)
	}
	var buf bytes.Buffer
	l.SetLogger(log.New(&buf, "", 0))
	t.Cleanup(func() { t.Logf("\n%s", buf.String()) })
	return l
}

func mustQuery(t *testing.T, l *Ledger, k int64) Report {
	t.Helper()
	r, err := l.Query(k)
	if err != nil {
		t.Fatalf("Query(%d): %v", k, err)
	}
	return r
}

func TestNewRejectsInvalidParameters(t *testing.T) {
	cases := []struct {
		t0, p, q, m int64
		want        error
	}{
		{0, 10, 0, 5, ErrInvalidBaseQuota},
		{0, 10, -3, 5, ErrInvalidBaseQuota},
		{0, 0, 10, 5, ErrInvalidPeriodLength},
		{0, -10, 10, 5, ErrInvalidPeriodLength},
		{0, 10, 10, -1, ErrNegativeCarryCap},
	}
	for _, c := range cases {
		if _, err := New(c.t0, c.p, c.q, c.m); !errors.Is(err, c.want) {
			t.Errorf("New(%d,%d,%d,%d) = %v, want %v",
				c.t0, c.p, c.q, c.m, err, c.want)
		}
	}
}

// A record whose right end lands exactly on a period boundary must not touch
// the next period.
func TestEndOnBoundaryDoesNotTouchNextPeriod(t *testing.T) {
	l := newLogged(t, 0, 10, 100, 0)
	if err := l.Add("a", 5, 10, 10); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if r := mustQuery(t, l, 0); r.Usage != 10 {
		t.Errorf("period0 usage = %d, want 10", r.Usage)
	}
	if r := mustQuery(t, l, 1); r.Usage != 0 {
		t.Errorf("period1 usage = %d, want 0 (boundary must not touch)", r.Usage)
	}
}

// floor(amount*overlap/length) shares leave a remainder; it goes entirely to
// the last touched period and the shares sum to amount.
func TestRemainderGoesToLastTouchedPeriod(t *testing.T) {
	// [0,30) spans three periods of length 10 with amount 10:
	// floor shares 3,3,3, remainder 1 -> last period holds 4.
	l := newLogged(t, 0, 10, 100, 0)
	if err := l.Add("a", 0, 30, 10); err != nil {
		t.Fatalf("Add: %v", err)
	}
	var sum int64
	var got [3]int64
	for k := range got {
		r := mustQuery(t, l, int64(k))
		got[k] = r.Usage
		sum += r.Usage
	}
	if got != [3]int64{3, 3, 4} {
		t.Errorf("usages = %v, want [3 3 4]", got)
	}
	if sum != 10 {
		t.Errorf("sum of shares = %d, want amount 10", sum)
	}
}

// Asymmetric overlaps also conserve the total, with remainder on last period.
func TestAsymmetricAllocationConservesAmount(t *testing.T) {
	// [5,20): overlaps 5 and 15, length 15, amount 11 -> floor 3,7 rem 1.
	l := newLogged(t, 0, 10, 100, 0)
	if err := l.Add("a", 5, 20, 11); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r0 := mustQuery(t, l, 0)
	r1 := mustQuery(t, l, 1)
	if r0.Usage != 3 || r1.Usage != 8 {
		t.Errorf("usages = %d,%d, want 3,8", r0.Usage, r1.Usage)
	}
	if r0.Usage+r1.Usage != 11 {
		t.Errorf("sum = %d, want 11", r0.Usage+r1.Usage)
	}
}

// Leftover quota is carried forward but never above the cap M.
func TestCarryoverCappedAtM(t *testing.T) {
	l := newLogged(t, 0, 10, 10, 4)
	if err := l.Add("a", 0, 10, 3); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if r := mustQuery(t, l, 1); r.CarryIn != 4 || r.Quota != 14 {
		t.Errorf("capped carry=%d quota=%d, want 4,14", r.CarryIn, r.Quota)
	}

	l2 := newLogged(t, 0, 10, 10, 20)
	if err := l2.Add("a", 0, 10, 3); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if r := mustQuery(t, l2, 1); r.CarryIn != 7 || r.Quota != 17 {
		t.Errorf("uncapped carry=%d quota=%d, want 7,17", r.CarryIn, r.Quota)
	}
}

// Usage above the effective quota is reported as overage and yields carry-in 0
// for the next period (never negative).
func TestOverageProducesNoNegativeCarry(t *testing.T) {
	l := newLogged(t, 0, 10, 10, 5)
	if err := l.Add("a", 0, 10, 13); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if r := mustQuery(t, l, 0); r.Overage != 3 {
		t.Errorf("period0 overage = %d, want 3", r.Overage)
	}
	r1 := mustQuery(t, l, 1)
	if r1.CarryIn != 0 || r1.Quota != 10 || r1.Overage != 0 {
		t.Errorf("period1 = %+v, want carry 0 quota 10 overage 0", r1)
	}
}

// A late record changes an already-queried period and its effect propagates
// through subsequent carryovers.
func TestLateRecordChangesPriorPeriodsAndPropagates(t *testing.T) {
	// Q=10, M=100: usage 4 in period0 -> leftover 6 carries into period1.
	l := newLogged(t, 0, 10, 10, 100)
	if err := l.Add("early", 0, 10, 4); err != nil {
		t.Fatalf("Add: %v", err)
	}
	before := mustQuery(t, l, 1)
	if before.CarryIn != 6 || before.Quota != 16 {
		t.Fatalf("before late record: carry=%d quota=%d, want 6,16",
			before.CarryIn, before.Quota)
	}

	// Late record adds 5 usage inside period0: leftover shrinks 6 -> 1.
	if err := l.Add("late", 2, 8, 5); err != nil {
		t.Fatalf("Add late: %v", err)
	}
	if r := mustQuery(t, l, 0); r.Usage != 9 {
		t.Errorf("period0 usage = %d, want 9", r.Usage)
	}
	r1 := mustQuery(t, l, 1)
	if r1.CarryIn != 1 || r1.Quota != 11 {
		t.Errorf("period1 carry=%d quota=%d, want 1,11", r1.CarryIn, r1.Quota)
	}
	// Period1 is empty, so its full effective quota rolls on to period2.
	r2 := mustQuery(t, l, 2)
	if r2.CarryIn != 11 || r2.Quota != 21 {
		t.Errorf("period2 carry=%d quota=%d, want 11,21", r2.CarryIn, r2.Quota)
	}
}

// The same record set arriving in different orders gives identical results.
func TestArrivalOrderIndependence(t *testing.T) {
	specs := []struct {
		id           string
		s, e, amount int64
	}{
		{"r1", 5, 25, 11},
		{"r2", 0, 10, 7},
		{"r3", 15, 35, 23},
		{"r4", 29, 31, 0},
	}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}}
	var baseline []Report
	for oi, order := range orders {
		l := newLogged(t, 0, 10, 12, 6)
		for _, idx := range order {
			sp := specs[idx]
			if err := l.Add(sp.id, sp.s, sp.e, sp.amount); err != nil {
				t.Fatalf("order %d Add %s: %v", oi, sp.id, err)
			}
		}
		var got []Report
		for k := int64(0); k < 4; k++ {
			got = append(got, mustQuery(t, l, k))
		}
		if baseline == nil {
			baseline = got
			continue
		}
		for k := range got {
			if got[k] != baseline[k] {
				t.Errorf("order %d period %d = %+v, want %+v",
					oi, k, got[k], baseline[k])
			}
		}
	}
}

func TestRejectionsPrecedenceAndAtomicity(t *testing.T) {
	l := newLogged(t, 0, 10, 10, 5)
	if err := l.Add("ok", 0, 10, 4); err != nil {
		t.Fatalf("Add: %v", err)
	}
	bad := []struct {
		id           string
		s, e, amount int64
		want         error
	}{
		{"bad1", -1, 10, 5, ErrStartBeforeT0},
		{"bad2", 10, 10, 5, ErrInvalidInterval},
		{"bad3", 5, 4, 5, ErrInvalidInterval},
		{"bad4", 0, 10, -1, ErrNegativeAmount},
		{"ok", 0, 10, 5, ErrDuplicateID},
	}
	for _, b := range bad {
		if err := l.Add(b.id, b.s, b.e, b.amount); !errors.Is(err, b.want) {
			t.Errorf("Add(%s) = %v, want %v", b.id, err, b.want)
		}
	}
	// Precedence: s<t0 beats interval beats amount beats duplicate.
	if err := l.Add("ok", -9, -8, -5); !errors.Is(err, ErrStartBeforeT0) {
		t.Errorf("precedence 1: got %v, want ErrStartBeforeT0", err)
	}
	if err := l.Add("ok", 20, 5, -5); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("precedence 2: got %v, want ErrInvalidInterval", err)
	}
	if err := l.Add("ok", 0, 10, -5); !errors.Is(err, ErrNegativeAmount) {
		t.Errorf("precedence 3: got %v, want ErrNegativeAmount", err)
	}

	if r := mustQuery(t, l, 0); r.Usage != 4 {
		t.Errorf("period0 usage = %d, want 4 (rejects must not change ledger)", r.Usage)
	}
	if _, err := l.Query(-1); !errors.Is(err, ErrNegativePeriod) {
		t.Errorf("Query(-1) = %v, want ErrNegativePeriod", err)
	}
}

func TestZeroAmountRecordIsLegal(t *testing.T) {
	l := newLogged(t, 0, 10, 10, 5)
	if err := l.Add("zero", 0, 10, 0); err != nil {
		t.Fatalf("zero-amount Add: %v", err)
	}
	if r := mustQuery(t, l, 0); r.Usage != 0 {
		t.Errorf("usage = %d, want 0", r.Usage)
	}
}

func TestConcurrentAddAndQuery(t *testing.T) {
	l := newLogged(t, 0, 10, 1000, 1000)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := int64((i % 4) * 10)
			_ = l.Add(fmt.Sprintf("g%d", i), s, s+15, int64(i))
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = l.Query(int64(i % 5))
		}()
	}
	wg.Wait()

	// Conservation: every accepted record's amount is allocated somewhere.
	var expected int64
	for i := 0; i < 20; i++ {
		expected += int64(i)
	}
	var total int64
	for k := int64(0); k < 6; k++ {
		total += mustQuery(t, l, k).Usage
	}
	if total != expected {
		t.Errorf("total usage = %d, want %d", total, expected)
	}
}
