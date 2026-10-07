package meshauthz

import "strings"

// patternKind classifies the four legal element forms.
type patternKind int

const (
	patternExact patternKind = iota
	patternPrefix
	patternSuffix
	patternAll
)

// pattern is a compiled string element. Identities, namespaces and
// paths all share these four forms; methods and ports do not use
// patterns at all (methods compare exactly, ports numerically).
type pattern struct {
	kind  patternKind
	value string
}

// compilePattern validates and compiles one element. The '*' wildcard
// may appear only at the start or only at the end, never both, and a
// lone '*' matches everything. Empty elements are rejected.
func compilePattern(s string) (pattern, error) {
	if s == "" {
		return pattern{}, errEmptyElement
	}
	if s == "*" {
		return pattern{kind: patternAll}, nil
	}
	prefixStar := strings.HasPrefix(s, "*")
	suffixStar := strings.HasSuffix(s, "*")
	switch {
	case prefixStar && suffixStar:
		return pattern{}, errStarBothEnds
	case prefixStar:
		body := s[1:]
		if strings.Contains(body, "*") {
			return pattern{}, errStarMiddle
		}
		return pattern{kind: patternSuffix, value: body}, nil
	case suffixStar:
		body := s[:len(s)-1]
		if strings.Contains(body, "*") {
			return pattern{}, errStarMiddle
		}
		return pattern{kind: patternPrefix, value: body}, nil
	default:
		if strings.Contains(s, "*") {
			return pattern{}, errStarMiddle
		}
		return pattern{kind: patternExact, value: s}, nil
	}
}

// matches reports whether the pattern accepts value.
func (p pattern) matches(value string) bool {
	switch p.kind {
	case patternAll:
		return true
	case patternExact:
		return p.value == value
	case patternPrefix:
		return strings.HasPrefix(value, p.value)
	case patternSuffix:
		return strings.HasSuffix(value, p.value)
	}
	return false
}

// matchStringSet applies the set semantics shared by every string
// field: an empty positive set imposes no constraint, otherwise the
// value must match at least one element (OR); a non-empty negative
// set rejects the value when it matches any element.
func matchStringSet(value string, positive, negative []pattern) bool {
	for _, n := range negative {
		if n.matches(value) {
			return false
		}
	}
	if len(positive) == 0 {
		return true
	}
	for _, p := range positive {
		if p.matches(value) {
			return true
		}
	}
	return false
}

// matchExactSet is the method analogue of matchStringSet: plain
// case-sensitive equality, no wildcards.
func matchExactSet(value string, positive, negative []string) bool {
	for _, n := range negative {
		if n == value {
			return false
		}
	}
	if len(positive) == 0 {
		return true
	}
	for _, p := range positive {
		if p == value {
			return true
		}
	}
	return false
}

// matchIntSet is the port analogue of matchStringSet.
func matchIntSet(value int, positive, negative []int) bool {
	for _, n := range negative {
		if n == value {
			return false
		}
	}
	if len(positive) == 0 {
		return true
	}
	for _, p := range positive {
		if p == value {
			return true
		}
	}
	return false
}
