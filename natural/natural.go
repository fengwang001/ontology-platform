// Package natural provides natural ("version-style") ordering for strings.
// Numeric runs are compared by numeric value; ties in numeric value are broken
// only after the rest of the strings has been compared, using leading-zero
// counts in run order. See DESIGN.md for the full derivation.
package natural

import (
	"bytes"
	"sort"

	"ontology/chunk"
)

var checked int // bytes inspected by the most recent Compare; unexported

// ComparedBytes reports the number of bytes inspected by the most recent
// Compare call.
func ComparedBytes() int { return checked }

// Compare returns -1, 0, or 1 when a is respectively less than, equal to, or
// greater than b under natural order. It returns 0 iff a == b.
func Compare(a, b string) int {
	checked = 0
	sa, sb := chunk.NewScanner(a), chunk.NewScanner(b)
	pa, pb := padAcc{}, padAcc{}
	prevA, prevB := 0, 0

	for {
		ha, hb := sa.Next(), sb.Next()
		checked += sa.Pos() - prevA + (sb.Pos() - prevB)
		prevA, prevB = sa.Pos(), sb.Pos()
		switch {
		case !ha && !hb:
			return pa.compare(&pb)
		case !ha:
			return -1
		case !hb:
			return 1
		}
		ta, tb := sa.Text(), sb.Text()
		checked += len(ta) + len(tb) // content-inspection pass (second scan at most)
		switch {
		case sa.Kind() == chunk.Other || sb.Kind() == chunk.Other:
			if c := bytes.Compare([]byte(ta), []byte(tb)); c != 0 {
				return c
			}
		default:
			na, za := trimZeros(ta)
			nb, zb := trimZeros(tb)
			if c := shortLex(na, nb); c != 0 {
				return c
			}
			pa.add(za)
			pb.add(zb)
		}
	}
}

// padAcc records leading-zero counts of numeric runs in encounter order.
type padAcc struct{ v []int }

func (p *padAcc) add(n int) { p.v = append(p.v, n) }

func (p *padAcc) compare(q *padAcc) int {
	for i := 0; i < len(p.v) && i < len(q.v); i++ {
		if p.v[i] != q.v[i] {
			if p.v[i] < q.v[i] {
				return -1
			}
			return 1
		}
	}
	if len(p.v) != len(q.v) {
		if len(p.v) < len(q.v) {
			return -1
		}
		return 1
	}
	return 0
}

// trimZeros returns the digit text after stripping leading zeros, plus the
// number of stripped zeros. The empty string represents numeric value zero.
func trimZeros(s string) (rest string, zeros int) {
	for zeros < len(s) && s[zeros] == '0' {
		zeros++
	}
	return s[zeros:], zeros
}

// shortLex compares equal-or-different length digit strings by length first,
// then lexicographically: the natural numeric order without integer parsing.
func shortLex(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// Less reports whether a sorts before b.
func Less(a, b string) bool { return Compare(a, b) < 0 }

// Sort orders s in place under natural order. The order is a strict total
// order, so the result for any multiset is unique regardless of input order.
func Sort(s []string) { sort.SliceStable(s, func(i, j int) bool { return Less(s[i], s[j]) }) }
