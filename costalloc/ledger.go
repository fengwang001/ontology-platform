// Package costalloc implements a step-down ledger that allocates service
// department costs to other departments once per closing period.
package costalloc

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"sync"
)

const (
	maxK     = 8
	maxP     = 8
	maxUsage = 1_000_000
	maxCost  = 10_000_000_000_000
)

var (
	// ErrInvalidParam reports any out-of-range argument. It is checked
	// before ErrNoReceiver.
	ErrInvalidParam = errors.New("costalloc: invalid parameter")
	// ErrNoReceiver reports a Close step whose total is positive while no
	// department can receive a share.
	ErrNoReceiver = errors.New("costalloc: positive total with no receiver")
)

// Share is one receiver's allocation in a single step.
type Share struct {
	Dept   int
	Amount int64
}

// Step describes one push-down step of a successful Close.
type Step struct {
	Dept   int     // service department pushed down at this step
	Total  int64   // T_s: direct cost plus shares received earlier this period
	Shares []Share // per-receiver amounts, sorted by Dept ascending
}

// Result is the outcome of a successful Close.
type Result struct {
	Steps     []Step  // one entry per service department, in push-down order
	FullCosts []int64 // full cost of production department K+i at index i
}

// Ledger is a concurrent-safe step-down cost allocation ledger.
type Ledger struct {
	mu      sync.Mutex
	k, p, n int
	u       [][]int64 // u[s][r]: service amount from s to r
	h       []int64   // H_r: cumulative amount received by r
	periods int
}

// New creates a ledger with k service departments (0..k-1) and p production
// departments (k..k+p-1).
func New(k, p int) (*Ledger, error) {
	if k < 1 || k > maxK || p < 1 || p > maxP {
		return nil, fmt.Errorf("%w: k=%d p=%d", ErrInvalidParam, k, p)
	}
	n := k + p
	u := make([][]int64, n)
	for i := range u {
		u[i] = make([]int64, n)
	}
	return &Ledger{k: k, p: p, n: n, u: u, h: make([]int64, n)}, nil
}

// SetUsage records that service department s provides amount u to department
// r, replacing any previous value. Usage persists across periods.
func (l *Ledger) SetUsage(s, r int, u int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 0 || s >= l.k {
		return fmt.Errorf("%w: s=%d is not a service department", ErrInvalidParam, s)
	}
	if r < 0 || r >= l.n || r == s {
		return fmt.Errorf("%w: r=%d out of range or equals s", ErrInvalidParam, r)
	}
	if u < 0 || u > maxUsage {
		return fmt.Errorf("%w: u=%d out of range", ErrInvalidParam, u)
	}
	l.u[s][r] = u
	return nil
}

// Close settles one period, pushing down every service department in a
// dynamically recomputed order. On any rejection the ledger is unchanged.
func (l *Ledger) Close(ds, dp []int64) (*Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(ds) != l.k || len(dp) != l.p {
		return nil, fmt.Errorf("%w: len(ds)=%d len(dp)=%d", ErrInvalidParam, len(ds), len(dp))
	}
	for _, v := range ds {
		if v < 0 || v > maxCost {
			return nil, fmt.Errorf("%w: ds value %d out of range", ErrInvalidParam, v)
		}
	}
	for _, v := range dp {
		if v < 0 || v > maxCost {
			return nil, fmt.Errorf("%w: dp value %d out of range", ErrInvalidParam, v)
		}
	}

	// Work on copies; commit only after every step succeeds.
	h := append([]int64(nil), l.h...)
	received := make([]int64, l.n) // shares received during this period
	pushed := make([]bool, l.k)
	steps := make([]Step, 0, l.k)

	for remaining := l.k; remaining > 0; remaining-- {
		s, b := l.selectNext(pushed)
		total := ds[s] + received[s]
		step := Step{Dept: s, Total: total}
		if total > 0 {
			if b == 0 {
				return nil, fmt.Errorf("%w: department %d total %d", ErrNoReceiver, s, total)
			}
			shares := l.allocate(s, total, b, pushed, h)
			for _, sh := range shares {
				h[sh.Dept] += sh.Amount
				received[sh.Dept] += sh.Amount
			}
			step.Shares = shares
		}
		pushed[s] = true
		steps = append(steps, step)
	}

	full := make([]int64, l.p)
	for i := 0; i < l.p; i++ {
		full[i] = dp[i] + received[l.k+i]
	}
	l.h = h
	l.periods++
	return &Result{Steps: steps, FullCosts: full}, nil
}

// Snapshot returns a copy of the cumulative received amounts H.
func (l *Ledger) Snapshot() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int64(nil), l.h...)
}

// Periods returns the number of successful Close calls.
func (l *Ledger) Periods() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.periods
}

// ratio computes a_s and b_s for an unpushed service department s given the
// current pushed set. b_s sums u[s][r] over every department r except s
// itself and already pushed service departments; a_s sums the part with
// r still unpushed service departments.
func (l *Ledger) ratio(s int, pushed []bool) (a, b int64) {
	for r := 0; r < l.n; r++ {
		if r == s {
			continue
		}
		if r < l.k && pushed[r] {
			continue
		}
		u := l.u[s][r]
		b += u
		if r < l.k {
			a += u
		}
	}
	return a, b
}

// selectNext picks the unpushed service department with the largest a/b,
// recomputed against the current pushed set. b == 0 counts as the smallest
// ratio; ties go to the smaller department number.
func (l *Ledger) selectNext(pushed []bool) (best int, bBest int64) {
	best = -1
	var aBest int64
	for s := 0; s < l.k; s++ {
		if pushed[s] {
			continue
		}
		a, b := l.ratio(s, pushed)
		if best == -1 || ratioGreater(a, b, aBest, bBest) {
			best, aBest, bBest = s, a, b
		}
	}
	return best, bBest
}

// ratioGreater reports whether a1/b1 is strictly greater than a2/b2, with a
// zero denominator treated as the smallest possible ratio. The comparison
// uses integer cross-multiplication.
func ratioGreater(a1, b1, a2, b2 int64) bool {
	if b1 == 0 {
		return false
	}
	if b2 == 0 {
		return true
	}
	return a1*b2 > a2*b1
}

// allocate computes the shares of total among the receivers of s and returns
// them sorted by department number. Receivers are all r with u[s][r] > 0
// that are neither s nor an already pushed service department. Each receiver
// gets floor(total*u[s][r]/b); the remainder goes one cent each to the first
// receivers ordered by (H_r ascending, department number ascending), with H
// taken at the start of this step.
func (l *Ledger) allocate(s int, total, b int64, pushed []bool, h []int64) []Share {
	type receiver struct {
		dept  int
		usage int64
	}
	recvs := make([]receiver, 0, l.n)
	for r := 0; r < l.n; r++ {
		if r == s {
			continue
		}
		if r < l.k && pushed[r] {
			continue
		}
		if l.u[s][r] > 0 {
			recvs = append(recvs, receiver{dept: r, usage: l.u[s][r]})
		}
	}
	shares := make([]Share, len(recvs))
	var sum int64
	for i, rc := range recvs {
		amount := int64(mulDiv64(uint64(total), uint64(rc.usage), uint64(b)))
		shares[i] = Share{Dept: rc.dept, Amount: amount}
		sum += amount
	}
	remainder := total - sum
	if remainder > 0 {
		order := make([]int, len(recvs))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(x, y int) bool {
			dx, dy := recvs[order[x]].dept, recvs[order[y]].dept
			if h[dx] != h[dy] {
				return h[dx] < h[dy]
			}
			return dx < dy
		})
		for i := int64(0); i < remainder; i++ {
			shares[order[i]].Amount++
		}
	}
	return shares
}

// mulDiv64 returns floor(a*b/d) using 128-bit intermediate arithmetic.
// Callers guarantee the quotient fits in uint64.
func mulDiv64(a, b, d uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	q, _ := bits.Div64(hi, lo, d)
	return q
}
