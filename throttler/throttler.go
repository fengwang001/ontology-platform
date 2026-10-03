// Package throttler implements a curve-based advertising budget throttler.
//
// A daily budget B is spread over n periods of length L according to
// per-period weights. The cumulative target curve tgt_i = floor(B*W_i/W_total)
// decides how much may be spent by the end of each period. Each period gets a
// fixed allowance A_i on the first accepted operation inside it, composed of
// the period quota q_i plus a catch-up amount bounded by floor(q_i*m/100),
// truncated by the remaining daily budget B - s_i.
package throttler

import (
	"errors"
	"math/bits"
	"sync"
)

var (
	ErrInvalidParam    = errors.New("throttler: invalid parameter")
	ErrClockBackwards  = errors.New("throttler: clock moved backwards")
	ErrBudgetExhausted = errors.New("throttler: daily budget exhausted")
	ErrAmountTooLarge  = errors.New("throttler: amount exceeds period allowance")
	ErrThrottled       = errors.New("throttler: throttled by period allowance")
	ErrRefundTooMuch   = errors.New("throttler: refund exceeds total spent")
)

const (
	maxBudget    int64 = 1_000_000_000_000
	maxPeriods         = 1440
	maxPeriodLen int64 = 1_000_000
	maxWeight    int64 = 1_000_000
	maxCatchUp   int64 = 10_000
	maxAmount    int64 = 1_000_000_000_000
)

// Throttler is safe for concurrent use; all operations behave as if executed
// in some serial order.
type Throttler struct {
	mu sync.Mutex

	budget    int64
	n         int
	periodLen int64
	catchUp   int64
	prefix    []int64 // prefix[i] = w_0 + ... + w_i
	totalW    int64

	spent  int64
	maxNow int64
	cur    int   // current period, -1 when no accepted operation yet
	allow  int64 // A_cur, fixed on the first accepted operation of the period
	ps     int64 // spent inside the current period

	tgtEvals int64 // test-only counter: tgt evaluations must not scan periods
}

// New validates the construction parameters and returns a Throttler.
func New(budget int64, n int, periodLen int64, weights []int64, catchUp int64) (*Throttler, error) {
	if budget < 1 || budget > maxBudget {
		return nil, ErrInvalidParam
	}
	if n < 1 || n > maxPeriods {
		return nil, ErrInvalidParam
	}
	if periodLen < 1 || periodLen > maxPeriodLen {
		return nil, ErrInvalidParam
	}
	if len(weights) != n {
		return nil, ErrInvalidParam
	}
	prefix := make([]int64, n)
	var acc int64
	for i, w := range weights {
		if w < 1 || w > maxWeight {
			return nil, ErrInvalidParam
		}
		acc += w
		prefix[i] = acc
	}
	if catchUp < 0 || catchUp > maxCatchUp {
		return nil, ErrInvalidParam
	}
	return &Throttler{
		budget:    budget,
		n:         n,
		periodLen: periodLen,
		catchUp:   catchUp,
		prefix:    prefix,
		totalW:    acc,
		cur:       -1,
	}, nil
}

// tgt returns floor(budget * prefix[i] / totalW) in O(1) using 128-bit
// arithmetic, since budget*prefix[i] can reach 1e21.
func (t *Throttler) tgt(i int) int64 {
	if i < 0 {
		return 0
	}
	t.tgtEvals++
	hi, lo := bits.Mul64(uint64(t.budget), uint64(t.prefix[i]))
	q, _ := bits.Div64(hi, lo, uint64(t.totalW))
	return int64(q)
}

// periodAllowance computes A_i for a period entered with spent == s.
func (t *Throttler) periodAllowance(s int64, i int) int64 {
	prev := t.tgt(i - 1)
	q := t.tgt(i) - prev
	d := prev - s
	if d < 0 {
		d = 0
	}
	cap_ := q * t.catchUp / 100
	if d < cap_ {
		cap_ = d
	}
	a := q + cap_
	if rem := t.budget - s; a > rem {
		a = rem
	}
	return a
}

func validAmount(a int64) bool { return a >= 1 && a <= maxAmount }

func (t *Throttler) validNow(now int64) bool {
	return now >= 0 && now < int64(t.n)*t.periodLen
}

// prepare returns the period index, allowance and period-spent that apply to
// now, without mutating any state. For a period that has no accepted
// operation yet, A_i is computed provisionally with s_i = spent and ps = 0.
func (t *Throttler) prepare(now int64) (i int, allow, ps int64, newPeriod bool) {
	i = int(now / t.periodLen)
	if i == t.cur {
		return i, t.allow, t.ps, false
	}
	return i, t.periodAllowance(t.spent, i), 0, true
}

func (t *Throttler) commitPeriod(i int, allow int64) {
	t.cur = i
	t.allow = allow
	t.ps = 0
}

// Try admits a spend of exactly a at time now.
func (t *Throttler) Try(a, now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validAmount(a) || !t.validNow(now) {
		return ErrInvalidParam
	}
	if now < t.maxNow {
		return ErrClockBackwards
	}
	i, allow, ps, newPeriod := t.prepare(now)
	if t.spent+a > t.budget {
		return ErrBudgetExhausted
	}
	if a > allow {
		return ErrAmountTooLarge
	}
	if ps+a > allow {
		return ErrThrottled
	}
	if newPeriod {
		t.commitPeriod(i, allow)
	}
	t.spent += a
	t.ps += a
	t.maxNow = now
	return nil
}

// TryUpTo admits x = min(a, remaining allowance of the period). It succeeds
// only when x >= 1, spending and returning x; otherwise it is throttled.
func (t *Throttler) TryUpTo(a, now int64) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validAmount(a) || !t.validNow(now) {
		return 0, ErrInvalidParam
	}
	if now < t.maxNow {
		return 0, ErrClockBackwards
	}
	i, allow, ps, newPeriod := t.prepare(now)
	x := a
	if rem := allow - ps; x > rem {
		x = rem
	}
	if x < 1 {
		return 0, ErrThrottled
	}
	if newPeriod {
		t.commitPeriod(i, allow)
	}
	t.spent += x
	t.ps += x
	t.maxNow = now
	return x, nil
}

// Refund returns a to the budget. A_i of the current period is fixed with the
// spent value before the refund; ps drops by min(ps, a) only.
func (t *Throttler) Refund(a, now int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !validAmount(a) || !t.validNow(now) {
		return ErrInvalidParam
	}
	if now < t.maxNow {
		return ErrClockBackwards
	}
	if a > t.spent {
		return ErrRefundTooMuch
	}
	i, allow, _, newPeriod := t.prepare(now)
	if newPeriod {
		t.commitPeriod(i, allow)
	}
	t.spent -= a
	if a < t.ps {
		t.ps -= a
	} else {
		t.ps = 0
	}
	t.maxNow = now
	return nil
}

// Allowance returns the remaining allowance A_i - ps of the period containing
// now. It is read-only and does not check the clock. A period without any
// accepted operation is evaluated with s_i = spent and ps = 0; a period
// earlier than the current one yields 0.
func (t *Throttler) Allowance(now int64) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.validNow(now) {
		return 0, ErrInvalidParam
	}
	i := int(now / t.periodLen)
	if i < t.cur {
		return 0, nil
	}
	if i == t.cur {
		return t.allow - t.ps, nil
	}
	return t.periodAllowance(t.spent, i), nil
}
