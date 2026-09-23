// Package natural provides natural ("human") ordering: numeric runs are
// compared by value without integer conversion, and equal numeric values
// defer to the following text before leading-zero count.
package natural

import (
	"sort"

	"ontology/chunk"
)

// checked counts bytes examined by the most recent Compare.
var checked int

// Checked returns the number of bytes examined by the most recent Compare.
func Checked() int { return checked }

// pending records a leading-zero tie between two digit segments.
type pending struct{ zerosA, zerosB int }

// Compare returns -1, 0, 1 like bytes.Compare under the natural total order.
// It returns 0 exactly when a == b.
func Compare(a, b string) int {
	checked = 0
	ia, ib := 0, 0
	var pend *pending
	for {
		sa, na := chunk.Next(a, ia)
		sb, nb := chunk.Next(b, ib)
		ia, ib = na, nb
		checked += len(sa.Text) + len(sb.Text)

		if sa.Text == "" || sb.Text == "" {
			// End-of-stream tag is smallest.
			if sa.Text == "" && sb.Text == "" {
				if pend != nil {
					return cmpInt(pend.zerosA, pend.zerosB)
				}
				return 0
			}
			if sa.Text == "" {
				return -1
			}
			return 1
		}

		if sa.Digit != sb.Digit {
			if sa.Digit {
				return -1 // digit tag < non-digit tag
			}
			return 1
		}

		if !sa.Digit {
			if c := compareText(sa.Text, sb.Text); c != 0 {
				return c
			}
			continue
		}

		za, ra := trimZero(sa.Text)
		zb, rb := trimZero(sb.Text)
		if c := compareDigits(ra, rb); c != 0 {
			return c // different values decide immediately
		}
		if pend == nil && za != zb {
			pend = &pending{za, zb} // defer zero count to the first numeric tie
		}
	}
}

// compareText compares two non-digit segments byte-wise; the segment that
// reaches its end (a type boundary) first is smaller.
func compareText(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return cmpInt(len(a), len(b))
}

// trimZero returns the leading-zero count and the remaining significant text.
func trimZero(s string) (int, string) {
	i := 0
	for i < len(s) && s[i] == '0' {
		i++
	}
	return i, s[i:]
}

// compareDigits compares two zero-free digit strings by value, using length
// then lexicographic bytes, so arbitrary-length numbers never overflow.
func compareDigits(a, b string) int {
	if c := cmpInt(len(a), len(b)); c != 0 {
		return c
	}
	for i := 0; i < len(a); i++ {
	if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func cmpInt(a, b int) int {
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

// Sort sorts s in place under the natural order.
func Sort(s []string) { sort.Slice(s, func(i, j int) bool { return Less(s[i], s[j]) }) }
