package match

import (
	"strings"
	"testing"
)

// TestStateCountLinear proves memoization collapses exponential
// backtracking into a polynomial DP: for a fixed pattern of length
// n, the number of evaluated (i, j) states never exceeds
// (n+1)*(m+1), i.e. grows at most linearly in m.
func TestStateCountLinear(t *testing.T) {
	p := "a*b*c*" // n = 6
	n := len(p)
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := strings.Repeat("a", m-1) + "b"
		d := newSolver(s, p)
		if !d.solve(0, 0) {
			t.Fatalf("m=%d: expected match", m)
		}
		if limit := (n + 1) * (m + 1); d.steps > limit {
			t.Fatalf("m=%d: evaluated %d states, limit %d", m, d.steps, limit)
		}
	}
}

// TestMemoizedEqualsNaive exhaustively compares the memoized DP
// against the naive backtracking oracle on every pattern up to
// length 5 over {a,b,.,*} and every text up to length 5 over {a,b}.
func TestMemoizedEqualsNaive(t *testing.T) {
	patterns := []string{""}
	for _, a := range []string{"a", "b", ".", "*"} {
		for _, b := range []string{"", "a", "b", ".", "*"} {
			for _, c := range []string{"", "a", "b", ".", "*"} {
				for _, d := range []string{"", "a", "b", ".", "*"} {
					for _, e := range []string{"", "a", "b", ".", "*"} {
						patterns = append(patterns, a+b+c+d+e)
					}
				}
			}
		}
	}
	texts := []string{""}
	for _, a := range []string{"a", "b"} {
		for _, b := range []string{"", "a", "b"} {
			for _, c := range []string{"", "a", "b"} {
				for _, d := range []string{"", "a", "b"} {
					for _, e := range []string{"", "a", "b"} {
						texts = append(texts, a+b+c+d+e)
					}
				}
			}
		}
	}
	checked := 0
	for _, p := range patterns {
		for _, s := range texts {
			if got, want := Match(s, p), Naive(s, p); got != want {
				t.Fatalf("Match(%q,%q)=%v, Naive=%v", s, p, got, want)
			}
			checked++
		}
	}
	t.Logf("compared %d (pattern, text) pairs", checked)
}
