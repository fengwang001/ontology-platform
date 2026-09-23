// Package natural provides natural ("version") ordering for strings:
// runs of ASCII digits are compared by numeric value, everything else by
// byte order. Numeric runs of equal value are distinguished only after the
// whole normalized string ties, by their number of leading zeros, so the
// ordering is a strict total order for distinct strings.
package natural

import (
	"sort"

	"ontology/chunk"
)

// bytesCompared counts the source bytes examined by the most recent Compare.
var bytesCompared int

// Compare returns -1, 0 or 1 like bytes.Compare. It returns 0 iff a == b.
func Compare(a, b string) int {
	bytesCompared = 0
	pa, pb := chunk.New(a), chunk.New(b)
	zeroSign := 0

	for {
		ha, hb := pa.Next(), pb.Next()
		bytesCompared += len(pa.Text()) + len(pb.Text())
		if !ha || !hb {
			if ha == hb {
				break
			}
			if ha {
				return 1
			}
			return -1
		}

		ta, tb := pa.Text(), pb.Text()
		switch {
		case pa.IsDigit() != pb.IsDigit():
			if pa.IsDigit() {
				return -1
			}
			return 1
		case !pa.IsDigit():
			if c := compareBytes(ta, tb); c != 0 {
				return c
			}
		default:
			za, ra := trimDigits(ta)
			zb, rb := trimDigits(tb)
			if len(ra) != len(rb) {
				if len(ra) < len(rb) {
					return -1
				}
				return 1
			}
			if c := compareBytes(ra, rb); c != 0 {
				return c
			}
			if zeroSign == 0 && za != zb {
				if za < zb {
					zeroSign = -1
				} else {
					zeroSign = 1
				}
			}
		}
	}

	return zeroSign
}

// Less reports whether a sorts before b.
func Less(a, b string) bool { return Compare(a, b) < 0 }

// BytesCompared reports the source bytes examined by the most recent Compare.
func BytesCompared() int { return bytesCompared }

// Sort orders s in place using natural ordering.
func Sort(s []string) { sort.Slice(s, func(i, j int) bool { return Less(s[i], s[j]) }) }

// trimDigits splits a digit run into its leading-zero count and the
// significant remainder (normalized to "0" for all-zero runs). It never
// converts the value to an integer.
func trimDigits(t string) (zeros int, rest string) {
	i := 0
	for i < len(t) && t[i] == '0' {
		i++
	}
	if i == len(t) {
		return i - 1, t[len(t)-1:]
	}
	return i, t[i:]
}

func compareBytes(a, b string) int {
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
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
