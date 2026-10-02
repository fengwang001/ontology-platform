package allocation

// A deliberately naive, independent implementation of the specification
// used only by the differential tests. Every multiplication and ratio
// comparison goes through math/big so that no 128-bit shortcut in the
// production code can hide an overflow.

import (
	"math/big"
	"sort"
)

type naiveLedger struct {
	k, p, n int
	u       [][]int64
	h       []int64
	// instrumentation for the differential test
	remEvents   int // steps whose remainder was positive
	acceptedCls int
}

func newNaive(k, p int) *naiveLedger {
	n := k + p
	u := make([][]int64, n)
	for i := range u {
		u[i] = make([]int64, n)
	}
	return &naiveLedger{k: k, p: p, n: n, u: u, h: make([]int64, n)}
}

func (nl *naiveLedger) setUsage(s, r int, u int64) error {
	if s < 0 || s >= nl.k || r < 0 || r >= nl.n || r == s || u < 0 || u > maxUsage {
		return ErrInvalidArgument
	}
	nl.u[s][r] = u
	return nil
}

type naiveStep struct {
	service int
	total   int64
	shares  []Share
}

// close returns (steps, fullCosts, err). err is ErrInvalidArgument or
// ErrNoRecipient; on error nothing changes.
func (nl *naiveLedger) close(ds, dp []int64) ([]naiveStep, []int64, error) {
	if len(ds) != nl.k || len(dp) != nl.p {
		return nil, nil, ErrInvalidArgument
	}
	for _, c := range ds {
		if c < 0 || c > maxCost {
			return nil, nil, ErrInvalidArgument
		}
	}
	for _, c := range dp {
		if c < 0 || c > maxCost {
			return nil, nil, ErrInvalidArgument
		}
	}

	h := append([]int64(nil), nl.h...)
	received := make([]int64, nl.n)
	pushed := make([]bool, nl.k)
	steps := make([]naiveStep, 0, nl.k)

	for left := nl.k; left > 0; left-- {
		// Recompute a_s/b_s for every unpushed s from scratch.
		best, bestA, bestB := -1, int64(0), int64(0)
		for s := 0; s < nl.k; s++ {
			if pushed[s] {
				continue
			}
			var a, b int64
			for r := 0; r < nl.n; r++ {
				if r == s || (r < nl.k && pushed[r]) {
					continue
				}
				b += nl.u[s][r]
				if r < nl.k {
					a += nl.u[s][r]
				}
			}
			if best == -1 || naiveRatioGreater(a, b, bestA, bestB) {
				best, bestA, bestB = s, a, b
			}
		}
		s := best
		total := ds[s] + received[s]
		step := naiveStep{service: s, total: total}
		if total > 0 {
			var recips []int
			var b int64
			for r := 0; r < nl.n; r++ {
				if r == s || (r < nl.k && pushed[r]) {
					continue
				}
				if nl.u[s][r] > 0 {
					recips = append(recips, r)
				}
				b += nl.u[s][r]
			}
			if len(recips) == 0 {
				return nil, nil, ErrNoRecipient
			}
			shares := make([]Share, len(recips))
			var sum int64
			for i, r := range recips {
				// floor(T * u / b) with big integers.
				num := new(big.Int).Mul(big.NewInt(total), big.NewInt(nl.u[s][r]))
				amt := new(big.Int).Quo(num, big.NewInt(b)).Int64()
				shares[i] = Share{Dept: r, Amount: amt}
				sum += amt
			}
			rem := total - sum
			if rem > 0 {
				nl.remEvents++
				ord := append([]int(nil), recips...)
				sort.Slice(ord, func(i, j int) bool {
					if h[ord[i]] != h[ord[j]] {
						return h[ord[i]] < h[ord[j]]
					}
					return ord[i] < ord[j]
				})
				for _, r := range ord[:rem] {
					for i := range shares {
						if shares[i].Dept == r {
							shares[i].Amount++
						}
					}
				}
			}
			for _, sh := range shares {
				h[sh.Dept] += sh.Amount
				received[sh.Dept] += sh.Amount
			}
			step.shares = shares
		}
		pushed[s] = true
		steps = append(steps, step)
	}

	full := make([]int64, nl.p)
	for i := 0; i < nl.p; i++ {
		full[i] = dp[i] + received[nl.k+i]
	}
	nl.h = h
	nl.acceptedCls++
	return steps, full, nil
}

func naiveRatioGreater(a1, b1, a2, b2 int64) bool {
	if b1 == 0 {
		return false
	}
	if b2 == 0 {
		return true
	}
	l := new(big.Int).Mul(big.NewInt(a1), big.NewInt(b2))
	r := new(big.Int).Mul(big.NewInt(a2), big.NewInt(b1))
	return l.Cmp(r) > 0
}
