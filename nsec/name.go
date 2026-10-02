// Package nsec implements a DNSSEC negative-answer cache that stores
// validated NSEC records as canonically ordered intervals and synthesizes
// NXDOMAIN / NODATA answers from them.
package nsec

import "strings"

const (
	maxNameLen  = 253
	maxLabelLen = 63
)

// name is a validated DNS name stored as canonical (ASCII lower-cased)
// labels in left-to-right order.
type name struct {
	labels []string
}

// parseName validates s and returns its canonical form. A single trailing
// dot is allowed and stripped. Labels are 1..63 bytes of [A-Za-z0-9_-*];
// the whole name (without the trailing dot) is 1..253 characters.
func parseName(s string) (name, bool) {
	if s == "" {
		return name{}, false
	}
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 || len(s) > maxNameLen {
		return name{}, false
	}
	parts := strings.Split(s, ".")
	labels := make([]string, len(parts))
	for i, p := range parts {
		if len(p) == 0 || len(p) > maxLabelLen {
			return name{}, false
		}
		b := []byte(p)
		for j, c := range b {
			switch {
			case c >= 'A' && c <= 'Z':
				b[j] = c + ('a' - 'A')
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9',
				c == '-', c == '_', c == '*':
			default:
				return name{}, false
			}
		}
		labels[i] = string(b)
	}
	return name{labels: labels}, true
}

func (n name) String() string { return strings.Join(n.labels, ".") }

// compareLabels is the DNSSEC canonical ordering: labels are compared from
// the rightmost one leftwards, each label byte-wise (shorter prefix first);
// when all shared labels are equal the name with fewer labels is smaller
// (ancestors sort before descendants).
func compareLabels(a, b []string) int {
	i, j := len(a)-1, len(b)-1
	for i >= 0 && j >= 0 {
		if a[i] != b[j] {
			if a[i] < b[j] {
				return -1
			}
			return 1
		}
		i--
		j--
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// commonSuffix counts the labels a and b share starting from the right.
// It performs plain label equality checks and is intentionally not counted
// as a name comparison.
func commonSuffix(a, b []string) int {
	n := 0
	for i, j := len(a)-1, len(b)-1; i >= 0 && j >= 0 && a[i] == b[j]; i, j = i-1, j-1 {
		n++
	}
	return n
}

// inZone reports whether n equals the zone apex or is a descendant of it.
func (n name) inZone(z name) bool {
	return commonSuffix(n.labels, z.labels) == len(z.labels)
}
