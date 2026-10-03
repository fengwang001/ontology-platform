// Package nsec implements a DNSSEC negative-answer cache that stores
// validated NSEC records as canonically ordered intervals and synthesizes
// NXDOMAIN / NODATA answers from them.
package nsec

import "strings"

const (
	maxNameLen  = 253
	maxLabelLen = 63

	// maxNow is the largest accepted timestamp (10^12 seconds).
	maxNow = 1_000_000_000_000
	// maxTTL bounds both record TTLs and the SOA negative-cache ceiling.
	maxTTL = 86400

	typeNSEC  = 47
	typeCNAME = 5
)

// Name is a validated, case-folded domain name. labels are ordered
// left-to-right (outermost first); key is the joined canonical form.
type Name struct {
	labels []string
	key    string
}

// parseName validates s and folds it to lowercase. A single trailing dot is
// allowed and stripped. Labels are 1..63 bytes of [A-Za-z0-9-_*]; the whole
// name (after stripping the trailing dot) must be 1..253 characters.
func parseName(s string) (Name, error) {
	if s == "" {
		return Name{}, ErrInvalidParam
	}
	if strings.HasSuffix(s, ".") {
		s = s[:len(s)-1]
	}
	if s == "" || len(s) > maxNameLen {
		return Name{}, ErrInvalidParam
	}
	parts := strings.Split(s, ".")
	labels := make([]string, len(parts))
	for i, p := range parts {
		if len(p) == 0 || len(p) > maxLabelLen {
			return Name{}, ErrInvalidParam
		}
		l, ok := foldLabel(p)
		if !ok {
			return Name{}, ErrInvalidParam
		}
		labels[i] = l
	}
	return Name{labels: labels, key: strings.Join(labels, ".")}, nil
}

// foldLabel lowercases ASCII letters and rejects any byte outside the
// allowed label alphabet.
func foldLabel(s string) (string, bool) {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + ('a' - 'A')
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '*':
		default:
			return "", false
		}
	}
	return string(b), true
}

// compareNames returns -1, 0 or +1 for the canonical DNSSEC name order:
// compare labels from the rightmost outward; labels compare bytewise
// (shorter prefix first); when all shared labels are equal the name with
// fewer labels (the ancestor) sorts first.
func compareNames(a, b Name) int {
	i, j := len(a.labels)-1, len(b.labels)-1
	for i >= 0 && j >= 0 {
		if c := strings.Compare(a.labels[i], b.labels[j]); c != 0 {
			return c
		}
		i--
		j--
	}
	switch {
	case len(a.labels) < len(b.labels):
		return -1
	case len(a.labels) > len(b.labels):
		return 1
	}
	return 0
}

// commonSuffix returns the number of labels a and b share counting from the
// right. It performs no canonical-order comparison and is not charged to
// the nameCmp counter.
func commonSuffix(a, b Name) int {
	i, j := len(a.labels)-1, len(b.labels)-1
	n := 0
	for i >= 0 && j >= 0 && a.labels[i] == b.labels[j] {
		n++
		i--
		j--
	}
	return n
}

// inZone reports whether zone is a suffix (ancestor-or-self) of n.
func (n Name) inZone(zone Name) bool {
	if len(n.labels) < len(zone.labels) {
		return false
	}
	off := len(n.labels) - len(zone.labels)
	for i, z := range zone.labels {
		if n.labels[off+i] != z {
			return false
		}
	}
	return true
}

// wildcardBelow returns "*.<rightmost k labels of n>". Callers must
// guarantee 1 <= k <= len(n.labels).
func (n Name) wildcardBelow(k int) Name {
	labels := make([]string, 0, k+1)
	labels = append(labels, "*")
	labels = append(labels, n.labels[len(n.labels)-k:]...)
	return Name{labels: labels, key: strings.Join(labels, ".")}
}
