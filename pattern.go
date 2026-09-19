// Package projection implements field-level read projection: given an object
// and a compiled set of allow/deny field rules, it produces the subset of the
// object visible to a caller while preserving structural constraints.
package projection

import (
	"fmt"
	"strings"
)

// wildcard is the single-level wildcard token. "addr.*" matches direct
// children of addr only; it never crosses a segment boundary.
const wildcard = "*"

// pattern is a parsed visibility rule pattern.
type pattern struct {
	raw      string // original rule text, used in explanations and errors
	segments []string
	wildcard bool // pattern ends with a single-level wildcard
}

// validatePattern parses and validates a raw rule pattern.
//
// Invalid patterns are: empty patterns, empty segments (e.g. "a..b" or
// ".a"), multi-level wildcards ("a.*.b", "*", "a.*.*"), wildcards mixed
// into a segment ("a*.b"), and characters outside [a-zA-Z0-9_-].
func validatePattern(raw string) (pattern, error) {
	if raw == "" {
		return pattern{}, fmt.Errorf("empty pattern")
	}
	parts := strings.Split(raw, ".")
	segs := make([]string, 0, len(parts))
	for i, p := range parts {
		if p == "" {
			return pattern{}, fmt.Errorf("empty segment at position %d in %q", i, raw)
		}
		if strings.Contains(p, wildcard) {
			if p != wildcard {
				return pattern{}, fmt.Errorf("illegal wildcard use in segment %q of %q", p, raw)
			}
			if i != len(parts)-1 {
				return pattern{}, fmt.Errorf("multi-level wildcard in %q: %q may only be the final segment", raw, wildcard)
			}
			continue
		}
		if !validName(p) {
			return pattern{}, fmt.Errorf("illegal character in segment %q of %q", p, raw)
		}
		segs = append(segs, p)
	}
	p := pattern{raw: raw, segments: segs}
	if parts[len(parts)-1] == wildcard {
		if len(segs) == 0 {
			return pattern{}, fmt.Errorf("wildcard %q requires a named prefix", wildcard)
		}
		p.wildcard = true
	}
	return p, nil
}

func validName(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// matchesDirect reports whether the pattern matches the field at path as a
// direct rule. Wildcard patterns match only direct children of their prefix;
// exact patterns match themselves. Wildcard patterns never match deeper
// descendants, so "addr.*" does not govern "addr.geo.lat".
func (p pattern) matchesDirect(path []string) bool {
	if p.wildcard {
		if len(path) != len(p.segments)+1 {
			return false
		}
		for i, s := range p.segments {
			if path[i] != s {
				return false
			}
		}
		return validName(path[len(path)-1])
	}
	if len(path) != len(p.segments) {
		return false
	}
	for i, s := range p.segments {
		if path[i] != s {
			return false
		}
	}
	return true
}

// specificity ranks how specific a direct match is: an exact pattern at the
// field's own depth (2) beats a single-level wildcard (1).
func (p pattern) specificity() int {
	if p.wildcard {
		return 1
	}
	return 2
}
