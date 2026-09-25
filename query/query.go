// Package query answers substring-equality and longest-common-prefix
// queries on top of rhash prefix tables.
package query

import "ontology/rhash"

// Querier answers read-only queries over one built table.
type Querier struct {
	t *rhash.Table
}

// New wraps a built table. The table must not be mutated afterwards.
func New(t *rhash.Table) *Querier { return &Querier{t: t} }

// Equal reports whether s[l1:r1) and s[l2:r2) are equal: lengths must
// match, then both moduli must agree. Ranges must be valid (checked by
// the caller in package api).
func (q *Querier) Equal(l1, r1, l2, r2 int) bool {
	if r1-l1 != r2-l2 {
		return false
	}
	a1, a2 := q.t.Hash(l1, r1)
	b1, b2 := q.t.Hash(l2, r2)
	return a1 == b1 && a2 == b2
}

// LCP returns the longest common prefix length of suffixes s[i:] and
// s[j:], found by binary search on the length using Equal. It also
// returns the number of hash comparisons used, which never exceeds
// ceil(log2 n) + 1. Indices must be valid (checked by package api).
func (q *Querier) LCP(i, j int) (length, comparisons int) {
	n := q.t.Len()
	hi := n - i
	if n-j < hi {
		hi = n - j
	}
	lo := 0
	for lo < hi {
		mid := (lo + hi + 1) / 2
		comparisons++
		if q.Equal(i, i+mid, j, j+mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, comparisons
}
