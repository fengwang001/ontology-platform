package semver

import "strings"

// Range is a conjunction of constraint groups. Each group corresponds to
// one source range string; prerelease gating is applied per group so that
// Intersect results match the AND of the original ranges exactly.
type Range struct {
	orig   string
	groups [][]constraint
}

// String restores the original range string.
func (r Range) String() string { return r.orig }

// ParseRange parses space-separated constraints, all of which must hold
// (logical AND). Supported forms: >=, >, <=, <, =, ^ and ~.
func ParseRange(s string) (Range, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Range{}, &ParseError{Kind: "range", Input: s, Err: ErrInvalidRange}
	}
	var cons []constraint
	for _, tok := range fields {
		c, err := parseConstraint(tok)
		if err != nil {
			return Range{}, err
		}
		cons = append(cons, c)
	}
	return Range{orig: s, groups: [][]constraint{cons}}, nil
}

// Match reports whether v satisfies every constraint group of the range.
//
// A prerelease version can only match a group when some constraint in
// that group carries an operand with the same major/minor/patch triple
// that is itself a prerelease, so plain ranges never pull in prereleases
// by accident.
func (r Range) Match(v Version) bool {
	for _, g := range r.groups {
		if !matchGroup(g, v) {
			return false
		}
	}
	return true
}

func matchGroup(g []constraint, v Version) bool {
	if v.IsPrerelease() && !groupAllowsPrerelease(g, v) {
		return false
	}
	for _, c := range g {
		if !c.match(v) {
			return false
		}
	}
	return true
}

// groupAllowsPrerelease reports whether any comparator operand in the
// group shares v's release triple and is itself a prerelease.
func groupAllowsPrerelease(g []constraint, v Version) bool {
	for _, c := range g {
		for _, cmp := range c.cmps {
			ov := cmp.v
			if ov.IsPrerelease() && ov.Major == v.Major &&
				ov.Minor == v.Minor && ov.Patch == v.Patch {
				return true
			}
		}
	}
	return false
}
