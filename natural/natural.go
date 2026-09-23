// Package natural implements a natural ordering on strings: runs of
// ASCII digits compare by numeric value, everything else bytewise.
// Compare is a strict total order: it returns 0 only for identical
// strings, so numerically equal runs with different leading zeros are
// ordered by a deferred tiebreak (fewer leading zeros first).
package natural

import (
	"slices"
	"strings"

	"ontology/chunk"
)

// lastBytes records how many bytes the most recent Compare examined.
var lastBytes int

// LastCompareBytes returns the number of bytes examined by the most
// recent Compare call. It is at most 2*(len(a)+len(b)): each byte is
// read once to find its chunk and at most once more to compare chunks.
func LastCompareBytes() int { return lastBytes }

// Compare returns -1, 0 or 1 as a orders before, equal to, or after b.
func Compare(a, b string) int {
	lastBytes = 0
	tie := 0 // deferred leading-zero tiebreak, first difference wins
	for len(a) > 0 && len(b) > 0 {
		ha, ra, da := chunk.Next(a)
		hb, rb, db := chunk.Next(b)
		lastBytes += len(ha) + len(hb)
		switch {
		case da && db:
			if c := compareNum(ha, hb); c != 0 {
				return c
			}
			if tie == 0 && len(ha) != len(hb) {
				tie = sign(len(ha) - len(hb))
			}
			a, b = ra, rb
		case !da && !db:
			n := min(len(ha), len(hb))
			if c := strings.Compare(ha[:n], hb[:n]); c != 0 {
				return c
			}
			// Equal common prefix: advance only past the common part so
			// the shorter chunk's successor meets the other's rest.
			a, b = a[n:], b[n:]
		default:
			return sign(int(a[0]) - int(b[0]))
		}
	}
	switch {
	case len(a) > 0:
		return 1
	case len(b) > 0:
		return -1
	}
	return tie
}

// compareNum compares two digit runs by value without converting to an
// integer, so runs of any length are safe. Equal values return 0 even
// if the texts differ in leading zeros.
func compareNum(x, y string) int {
	lastBytes += len(x) + len(y)
	x = strings.TrimLeft(x, "0")
	y = strings.TrimLeft(y, "0")
	if len(x) != len(y) {
		return sign(len(x) - len(y))
	}
	return strings.Compare(x, y)
}

// Less reports whether a orders before b.
func Less(a, b string) bool { return Compare(a, b) < 0 }

// Sort sorts s in natural order. Because Compare is a strict total
// order, the result is uniquely determined by the multiset of elements.
func Sort(s []string) { slices.SortFunc(s, Compare) }

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}
