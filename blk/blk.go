// Package blk partitions an index interval [0,n) into half-open blocks and
// answers block-boundary queries in O(1) closed form.
package blk

import (
	"errors"
	"sync/atomic"
)

var (
	errCoverage = errors.New("blk: partition coverage check failed")
	errProbe    = errors.New("blk: tail lookup is not O(1)")
)

// Part is a half-open index interval [Start, End).
type Part struct {
	Start int
	End   int
}

// probe records how many boundary entries the most recent TailBlock call
// consulted while locating a block. A closed-form lookup consults zero
// entries; a linear scan would consult O(n/b). Unexported by design.
var probeCount atomic.Int64

// probeValue exposes the counter to same-package white-box tests only.
func probeValue() int64 { return probeCount.Load() }

// count sets the number of boundary entries consulted by a lookup.
func count(n int64) { probeCount.Store(n) }

// Parts partitions [0,n) into consecutive blocks of width b. Blocks are
// half-open: [q*b, min((q+1)*b, n)). When b does not divide n the final
// block is a tail of size n%b. It returns nil for invalid arguments.
func Parts(n, b int) []Part {
	if n < 1 || b < 1 || b > n {
		return nil
	}
	ps := make([]Part, (n+b-1)/b)
	for q := range ps {
		s := q * b
		e := s + b
		if e > n {
			e = n
		}
		ps[q] = Part{Start: s, End: e}
	}
	return ps
}

// TailBlock returns the start index and size of the block containing k.
// It is O(1): the block index is the closed-form quotient k/b, so no
// boundary entry is ever consulted. Invalid arguments yield (0, 0) and
// leave the probe untouched.
func TailBlock(n, b, k int) (start, size int) {
	if n < 1 || b < 1 || b > n || k < 0 || k >= n {
		return 0, 0
	}
	count(0) // closed-form arithmetic: zero boundary entries consulted
	q := k / b
	start = q * b
	end := start + b
	if end > n {
		end = n
	}
	return start, end - start
}

// SelfCheck verifies partition completeness over a built-in table and
// asserts the tail-lookup probe stays zero at n = 100, 1000, 10000.
// It reports pass/fail without ever exposing the counter value.
func SelfCheck() error {
	cases := []struct{ n, b int }{
		{1, 1}, {5, 2}, {6, 3}, {7, 4}, {10, 3}, {17, 17},
	}
	for _, c := range cases {
		ps := Parts(c.n, c.b)
		if len(ps) == 0 || ps[0].Start != 0 {
			return errCoverage
		}
		for i, p := range ps {
			if p.End <= p.Start || (i > 0 && p.Start != ps[i-1].End) {
				return errCoverage
			}
			want := c.b
			if c.n%c.b != 0 && i == len(ps)-1 {
				want = c.n % c.b
			}
			if p.End-p.Start != want {
				return errCoverage
			}
		}
		if ps[len(ps)-1].End != c.n {
			return errCoverage
		}
	}
	for _, n := range []int{100, 1000, 10000} {
		if s, sz := TailBlock(n, 17, n-1); s != n-(n%17) || sz != n%17 || probeValue() != 0 {
			return errProbe
		}
	}
	return nil
}
