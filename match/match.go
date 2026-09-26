// Package match implements the memoized dynamic-programming
// matcher. It depends on parse for pattern validation.
package match

import "ontology/parse"

// solver evaluates one (s, p) pair. memo caches finished states;
// states counts how many (i, j) states were actually evaluated.
// Both are per-call and non-exported: no exported function or
// method ever exposes the counter.
type solver struct {
	s, p  string
	memo  [][]int8 // 0 = unknown, 1 = false, 2 = true
	steps int      // non-exported counter of evaluated states
}

// Match reports whether pattern p matches the whole of s
// (anchored at both ends). Invalid patterns match nothing.
func Match(s, p string) bool {
	if parse.Validate(p) != nil {
		return false
	}
	d := newSolver(s, p)
	return d.solve(0, 0)
}

func newSolver(s, p string) *solver {
	memo := make([][]int8, len(s)+1)
	for i := range memo {
		memo[i] = make([]int8, len(p)+1)
	}
	return &solver{s: s, p: p, memo: memo}
}

// solve computes dp[i][j]: does p[j:] match s[i:] entirely?
// The recurrence is identical to Naive's; memoization only caches
// results, never changes them.
func (d *solver) solve(i, j int) bool {
	if v := d.memo[i][j]; v != 0 {
		return v == 2
	}
	d.steps++
	res := d.eval(i, j)
	if res {
		d.memo[i][j] = 2
	} else {
		d.memo[i][j] = 1
	}
	return res
}

// eval is the raw recurrence, shared in shape with Naive.
func (d *solver) eval(i, j int) bool {
	if j == len(d.p) {
		return i == len(d.s)
	}
	first := i < len(d.s) && (d.p[j] == '.' || d.p[j] == d.s[i])
	if j+1 < len(d.p) && d.p[j+1] == '*' {
		// Zero repetitions, or one more repetition of the element.
		return d.solve(i, j+2) || first && d.solve(i+1, j)
	}
	return first && d.solve(i+1, j+1)
}

// Naive is the reference exponential backtracking matcher: for '*'
// it enumerates zero, one, or more repetitions and backtracks. It
// exists only as the correctness oracle for tests and SelfCheck;
// keep its inputs small.
func Naive(s, p string) bool {
	if parse.Validate(p) != nil {
		return false
	}
	return naive(s, p)
}

func naive(s, p string) bool {
	if p == "" {
		return s == ""
	}
	first := s != "" && (p[0] == '.' || p[0] == s[0])
	if len(p) >= 2 && p[1] == '*' {
		return naive(s, p[2:]) || first && naive(s[1:], p)
	}
	return first && naive(s[1:], p[1:])
}
