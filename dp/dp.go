// Package dp computes the longest common substring of a fixed reference
// string and arbitrary query strings using a single rolling DP row of
// O(min(|a|,|b|)) cells. It depends on no other package.
package dp

import (
	"errors"
	"strings"
	"sync/atomic"
)

// ErrSelfCheck is returned by SelfCheck when any built-in check fails.
var ErrSelfCheck = errors.New("dp: self-check failed")

// Solver holds the fixed reference string a. The rolling row itself is
// allocated per Solve call, so a Solver is safe for concurrent use.
type Solver struct {
	a string
	// cells records the number of DP cells retained by the most recent
	// Solve (the rolling-row size). It is unexported on purpose.
	cells atomic.Int64
}

// New creates a Solver for reference string a.
func New(a string) *Solver { return &Solver{a: a} }

// Solve returns the length of the longest common substring of a and b and
// its 0-based start index in a. Ties on length keep the smallest start.
// The shorter string always indexes the single rolling row.
func (s *Solver) Solve(b string) (length, start int) {
	a := s.a
	n, m := len(a), len(b)
	aShort := n <= m
	k, lim := min(n, m), max(n, m)
	row := make([]int, k+1) // retained cells: min(n,m)+1
	best, bestStart := 0, 0
	for p := 1; p <= lim; p++ { // prefix position along the longer string
		diag := 0
		for q := 1; q <= k; q++ { // prefix position along the shorter string
			prev := row[q]
			var ca, cb byte
			var ap int // prefix position into a of the current char
			if aShort {
				ca, cb, ap = a[q-1], b[p-1], q
			} else {
				ca, cb, ap = a[p-1], b[q-1], p
			}
			if ca == cb {
				v := diag + 1
				row[q] = v
				if st := ap - v; v > best || (v == best && st < bestStart) {
					best, bestStart = v, st
				}
			} else {
				row[q] = 0
			}
			diag = prev
		}
	}
	s.cells.Store(int64(len(row)))
	return best, bestStart
}

// fullTable fills the complete O(n*m) table and applies the same tie rule.
func fullTable(a, b string) (length, start int) {
	n, m := len(a), len(b)
	tab := make([][]int, n+1)
	for i := range tab {
		tab[i] = make([]int, m+1)
	}
	best, bestStart := 0, 0
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				tab[i][j] = tab[i-1][j-1] + 1
				if st := i - tab[i][j]; tab[i][j] > best ||
					(tab[i][j] == best && st < bestStart) {
					best, bestStart = tab[i][j], st
				}
			}
		}
	}
	return best, bestStart
}

// naive enumerates every start pair (i, j) and extends to the first
// mismatch, taking the greatest extension length (smallest start on ties).
func naive(a, b string) (length, start int) {
	best, bestStart := 0, 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best, bestStart = k, i
			}
		}
	}
	return best, bestStart
}

// SelfCheck verifies built-in pairs against both the full O(n*m) table and
// the naive enumeration, and asserts the retained-cell count stays
// min(n,m)+1 and stops growing with m once m exceeds n. It exposes no
// counter value, only pass/fail.
func SelfCheck() error {
	pairs := [][2]string{
		{"banana", "ananas"},
		{"abcx", "abc"},
		{"abcde", "abfce"},
		{"ababa", "aba"},
		{"aaaa", "aaa"},
		{"x", "y"},
	}
	for _, p := range pairs {
		got := New(p[0])
		l0, s0 := got.Solve(p[1])
		l1, s1 := fullTable(p[0], p[1])
		l2, s2 := naive(p[0], p[1])
		if l0 != l1 || s0 != s1 || l0 != l2 || s0 != s2 {
			return ErrSelfCheck
		}
	}

	const n = 100
	sol := New(strings.Repeat("ab", n/2))
	for _, m := range []int{100, 500, 1000, 10000} {
		b := strings.Repeat("ba", m/2)
		l0, _ := sol.Solve(b)
		l1, _ := naive(sol.a, b)
		if l0 != l1 || int(sol.cells.Load()) != min(n, m)+1 {
			return ErrSelfCheck
		}
	}
	if l3, _ := New(strings.Repeat("x", n)).Solve("xy"); l3 != 1 {
		return ErrSelfCheck
	}
	return nil
}
