// Package check provides reference implementations for cross-checking
// segtree: a naive array, and a stale lazy tree that only pushdowns on
// update (never on query) to pin the query-pushdown rule.
package check

// Naive applies range adds directly to a plain array.
type Naive struct{ a []int64 }

func NewNaive(n int) *Naive { return &Naive{make([]int64, n)} }

func (v *Naive) Add(l, r int, d int64) {
	for i := l; i <= r; i++ {
		v.a[i] += d
	}
}

func (v *Naive) Sum(l, r int) int64 {
	var s int64
	for i := l; i <= r; i++ {
		s += v.a[i]
	}
	return s
}

// Stale is a lazy segment tree whose query never pushdowns, so it may
// read stale child sums. It exists to pin the incident bug.
type Stale struct {
	n         int
	sum, lazy []int64
}

func NewStale(n int) *Stale { return &Stale{n: n, sum: make([]int64, 4*n), lazy: make([]int64, 4*n)} }

func (s *Stale) Add(l, r int, d int64) { s.add(1, 0, s.n-1, l, r, d) }

func (s *Stale) add(i, nl, nr, l, r int, d int64) {
	if l <= nl && nr <= r {
		s.sum[i] += int64(nr-nl+1) * d
		s.lazy[i] += d
		return
	}
	m := (nl + nr) / 2
	if s.lazy[i] != 0 { // pushdown on update only
		s.add(2*i, nl, m, nl, m, s.lazy[i])
		s.add(2*i+1, m+1, nr, m+1, nr, s.lazy[i])
		s.lazy[i] = 0
	}
	if l <= m {
		s.add(2*i, nl, m, l, r, d)
	}
	if r > m {
		s.add(2*i+1, m+1, nr, l, r, d)
	}
	s.sum[i] = s.sum[2*i] + s.sum[2*i+1]
}

func (s *Stale) Sum(l, r int) int64 { return s.query(1, 0, s.n-1, l, r) }

func (s *Stale) query(i, nl, nr, l, r int) int64 { // no pushdown: stale reads
	if l <= nl && nr <= r {
		return s.sum[i]
	}
	m := (nl + nr) / 2
	var v int64
	if l <= m {
		v += s.query(2*i, nl, m, l, r)
	}
	if r > m {
		v += s.query(2*i+1, m+1, nr, l, r)
	}
	return v
}
