// Package allocation implements a step-down service-department cost
// allocation ledger. Each closing period pushes service department costs
// down to not-yet-pushed departments in a dynamically recomputed order,
// with remainders assigned by cumulative historical shares.
package allocation

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"sync"
)

// Sentinel errors so callers can distinguish rejection causes.
var (
	// ErrInvalidArgument reports out-of-range or malformed parameters.
	ErrInvalidArgument = errors.New("allocation: invalid argument")
	// ErrNoRecipient reports a Close step whose total is positive but
	// which has no eligible recipient.
	ErrNoRecipient = errors.New("allocation: no recipient for positive total")
)

const (
	maxUsage = int64(1_000_000)
	maxCost  = int64(10_000_000_000_000) // 1e13, unit: cents
)

// Share is one recipient's allocation in a single push-down step.
type Share struct {
	Dept   int
	Amount int64
}

// Step describes one push-down: which service department was pushed,
// its total allocatable cost, and what each recipient received.
type Step struct {
	Service int
	Total   int64
	Shares  []Share
}

// CloseResult is the outcome of one successful Close.
type CloseResult struct {
	Steps     []Step  // in push-down order, exactly K entries
	FullCosts []int64 // per production department, length P
}

// Ledger is a cost allocation ledger. All methods are safe for
// concurrent use; the result is equivalent to some serial order.
type Ledger struct {
	mu sync.Mutex
	k  int
	p  int
	n  int
	u  [][]int64 // u[s][r]: service amount from s to r, persists across periods
	h  []int64   // h[r]: cumulative amount received by r over all successful Closes
}

// NewLedger creates a ledger with k service departments (ids 0..k-1)
// and p production departments (ids k..k+p-1). Both must be in [1, 8].
func NewLedger(k, p int) (*Ledger, error) {
	if k < 1 || k > 8 || p < 1 || p > 8 {
		return nil, ErrInvalidArgument
	}
	n := k + p
	u := make([][]int64, n)
	for i := range u {
		u[i] = make([]int64, n)
	}
	return &Ledger{k: k, p: p, n: n, u: u, h: make([]int64, n)}, nil
}

// SetUsage records that service department s provides amount u of
// service to department r, overwriting any previous value.
func (l *Ledger) SetUsage(s, r int, u int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 0 || s >= l.k {
		return fmt.Errorf("%w: %d is not a service department", ErrInvalidArgument, s)
	}
	if r < 0 || r >= l.n || r == s {
		return fmt.Errorf("%w: invalid recipient %d", ErrInvalidArgument, r)
	}
	if u < 0 || u > maxUsage {
		return fmt.Errorf("%w: usage %d out of range", ErrInvalidArgument, u)
	}
	l.u[s][r] = u
	return nil
}

// History returns a copy of the cumulative received amounts H.
func (l *Ledger) History() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int64(nil), l.h...)
}

// Close settles one period. ds holds the K service departments' direct
// costs, dp the P production departments' direct costs.
//
// The period is simulated on a working copy of H and committed only
// when every step succeeds, so a rejected Close changes no state.
func (l *Ledger) Close(ds, dp []int64) (*CloseResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(ds) != l.k || len(dp) != l.p {
		return nil, fmt.Errorf("%w: need %d service and %d production costs, got %d and %d",
			ErrInvalidArgument, l.k, l.p, len(ds), len(dp))
	}
	for _, c := range ds {
		if c < 0 || c > maxCost {
			return nil, fmt.Errorf("%w: cost %d out of range", ErrInvalidArgument, c)
		}
	}
	for _, c := range dp {
		if c < 0 || c > maxCost {
			return nil, fmt.Errorf("%w: cost %d out of range", ErrInvalidArgument, c)
		}
	}

	h := append([]int64(nil), l.h...) // working copy, committed on success
	received := make([]int64, l.n)    // amounts received this period
	pushed := make([]bool, l.k)       // pushed-down service departments

	res := &CloseResult{Steps: make([]Step, 0, l.k)}
	for left := l.k; left > 0; left-- {
		s, b := l.pickNext(pushed)
		total := ds[s] + received[s]
		step := Step{Service: s, Total: total}
		if total > 0 {
			recips := l.recipients(s, pushed)
			if len(recips) == 0 {
				return nil, fmt.Errorf("%w: department %d has total %d but no recipient",
					ErrNoRecipient, s, total)
			}
			shares := distribute(total, recips, l.u[s], b, h)
			for _, sh := range shares {
				h[sh.Dept] += sh.Amount
				received[sh.Dept] += sh.Amount
			}
			step.Shares = shares
		}
		pushed[s] = true
		res.Steps = append(res.Steps, step)
	}

	res.FullCosts = make([]int64, l.p)
	for i := 0; i < l.p; i++ {
		res.FullCosts[i] = dp[i] + received[l.k+i]
	}
	l.h = h
	return res, nil
}

// pickNext recomputes, for every unpushed service department s, the
// ratio a_s/b_s over the current sets and returns the argmax.
// b_s sums u[s][r] over all departments except s itself and already
// pushed service departments; a_s is the part going to unpushed
// service departments. b_s == 0 counts as the smallest ratio.
// Ties are broken by the smaller department id.
func (l *Ledger) pickNext(pushed []bool) (best int, bestB int64) {
	sel := -1
	var selA, selB int64
	for s := 0; s < l.k; s++ {
		if pushed[s] {
			continue
		}
		var a, b int64
		for r := 0; r < l.n; r++ {
			if r == s || (r < l.k && pushed[r]) {
				continue
			}
			b += l.u[s][r]
			if r < l.k {
				a += l.u[s][r]
			}
		}
		if sel == -1 || ratioGreater(a, b, selA, selB) {
			sel, selA, selB = s, a, b
		}
	}
	return sel, selB
}

// ratioGreater reports whether a1/b1 > a2/b2 via integer cross
// multiplication; a zero denominator counts as the smallest ratio.
func ratioGreater(a1, b1, a2, b2 int64) bool {
	if b1 == 0 {
		return false
	}
	if b2 == 0 {
		return true
	}
	return a1*b2 > a2*b1
}

// recipients lists every department r with u[s][r] > 0 that is neither
// s itself nor an already pushed service department.
func (l *Ledger) recipients(s int, pushed []bool) []int {
	var recips []int
	for r := 0; r < l.n; r++ {
		if r == s || (r < l.k && pushed[r]) {
			continue
		}
		if l.u[s][r] > 0 {
			recips = append(recips, r)
		}
	}
	return recips
}

// distribute splits total among recipients proportionally to u[s][r]
// over denominator b. The remainder (fewer cents than recipients) goes
// one cent each to the recipients with the smallest cumulative H at
// the start of this step, ties broken by department id.
func distribute(total int64, recips []int, usage []int64, b int64, h []int64) []Share {
	shares := make([]Share, len(recips))
	var sum int64
	for i, r := range recips {
		amt := mulDiv64(uint64(total), uint64(usage[r]), uint64(b))
		shares[i] = Share{Dept: r, Amount: int64(amt)}
		sum += int64(amt)
	}
	rem := total - sum
	if rem > 0 {
		order := append([]int(nil), recips...)
		sort.Slice(order, func(i, j int) bool {
			if h[order[i]] != h[order[j]] {
				return h[order[i]] < h[order[j]]
			}
			return order[i] < order[j]
		})
		byDept := make(map[int]int, len(recips))
		for i, r := range recips {
			byDept[r] = i
		}
		for _, r := range order[:int(rem)] {
			shares[byDept[r]].Amount++
		}
	}
	return shares
}

// mulDiv64 returns floor(x*y/d) using 128-bit arithmetic. The caller
// guarantees the quotient fits in 64 bits (here: y <= d, so the result
// never exceeds x).
func mulDiv64(x, y, d uint64) uint64 {
	hi, lo := bits.Mul64(x, y)
	q, _ := bits.Div64(hi, lo, d)
	return q
}
