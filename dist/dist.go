// Package dist implements the banded Levenshtein DP core.
package dist

import (
	"errors"
	"sync/atomic"
)

// ErrExceedsCap is returned when the true edit distance is greater than k.
var ErrExceedsCap = errors.New("dist: edit distance exceeds cap")

// lastCells records how many DP cells the most recent Distance call actually
// computed. Unexported on purpose: no exported function or method may reveal
// it; only same-package tests can observe it.
var lastCells atomic.Int64

// Distance returns the Levenshtein distance between a and b (insert, delete,
// replace each cost 1) when it is at most k; otherwise ErrExceedsCap.
// Only cells with |i-j| <= k are computed, which cannot change any result
// that is <= k. Requires k >= 0.
func Distance(a, b string, k int) (int, error) {
	n, m := len(a), len(b)
	if k < 0 || m-n > k || n-m > k {
		lastCells.Store(0)
		return 0, ErrExceedsCap
	}
	inf := k + 1 // sentinel: "greater than k", exact value irrelevant
	prev := make([]int, m+1)
	curr := make([]int, m+1)
	for j := range prev {
		prev[j] = inf
	}
	for j := 0; j <= k && j <= m; j++ { // row 0: dp[0][j] = j inside the band
		prev[j] = j
	}
	var cells int64
	for i := 1; i <= n; i++ {
		lo, hi := i-k, i+k
		if lo < 0 {
			lo = 0
		}
		if hi > m {
			hi = m
		}
		if lo > 0 {
			curr[lo-1] = inf // left sentinels keep out-of-band reads at "too big"
		}
		if hi < m {
			curr[hi+1] = inf
		}
		for j := lo; j <= hi; j++ {
			if j == 0 {
				curr[j] = i // dp[i][0] = i
				continue
			}
			best := prev[j] + 1 // delete a[i-1]
			if v := curr[j-1] + 1; v < best {
				best = v // insert b[j-1]
			}
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			if v := prev[j-1] + cost; v < best {
				best = v // match or replace
			}
			if best > inf {
				best = inf
			}
			curr[j] = best
		}
		cells += int64(hi - lo + 1)
		prev, curr = curr, prev
	}
	lastCells.Store(cells)
	if d := prev[m]; d <= k {
		return d, nil
	}
	return 0, ErrExceedsCap
}
