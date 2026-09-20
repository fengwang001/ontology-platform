package semver

import "strings"

// Intersect computes the conjunction of the given ranges. The result's
// Match is exactly equivalent to ANDing the Match of each input range.
// If the intersection is empty it returns a *ConflictError naming the
// two conflicting constraints as originally written.
func Intersect(rs ...Range) (Range, error) {
	var merged Range
	var origs []string
	for _, r := range rs {
		merged.groups = append(merged.groups, r.groups...)
		if r.orig != "" {
			origs = append(origs, r.orig)
		}
	}
	merged.orig = strings.Join(origs, " && ")
	if err := merged.checkSatisfiable(); err != nil {
		return Range{}, err
	}
	return merged, nil
}

// bound is one end of the precedence interval imposed by the comparators.
type bound struct {
	v      Version
	strict bool
	orig   string
}

// checkSatisfiable returns a *ConflictError if no version can match r.
func (r Range) checkSatisfiable() error {
	var cmps []comparator
	var owners []string
	for _, g := range r.groups {
		for _, c := range g {
			for _, cmp := range c.cmps {
				cmps = append(cmps, cmp)
				owners = append(owners, c.orig)
			}
		}
	}
	// Equality constraints pin a single version: verify it directly.
	var eq *Version
	eqOrig := ""
	for i, c := range cmps {
		if c.op != opEQ {
			continue
		}
		if eq != nil && Compare(*eq, c.v) != 0 {
			return &ConflictError{A: eqOrig, B: owners[i]}
		}
		v := c.v
		eq = &v
		eqOrig = owners[i]
	}
	if eq != nil {
		if r.Match(*eq) {
			return nil
		}
		return &ConflictError{A: eqOrig, B: r.blame(*eq)}
	}
	lower, upper, hasLower, hasUpper := bounds(cmps, owners)
	for _, cand := range candidates(lower, upper, hasLower, hasUpper, cmps) {
		if r.Match(cand) {
			return nil
		}
	}
	a, b := upper.orig, upper.orig
	if hasLower {
		a = lower.orig
	}
	return &ConflictError{A: a, B: b}
}

// bounds reduces all comparators to the strongest lower and upper bound.
func bounds(cmps []comparator, owners []string) (lower, upper bound, hasLower, hasUpper bool) {
	for i, c := range cmps {
		switch c.op {
		case opGT, opGE:
			strict := c.op == opGT
			if !hasLower || Compare(c.v, lower.v) > 0 ||
				Compare(c.v, lower.v) == 0 && strict {
				lower, hasLower = bound{c.v, strict, owners[i]}, true
			}
		case opLT, opLE:
			strict := c.op == opLT
			if !hasUpper || Compare(c.v, upper.v) < 0 ||
				Compare(c.v, upper.v) == 0 && strict {
				upper, hasUpper = bound{c.v, strict, owners[i]}, true
			}
		}
	}
	return lower, upper, hasLower, hasUpper
}

// candidates generates versions such that the range is satisfiable iff
// one of them matches: interval endpoints, the smallest release above
// the lower bound, and the smallest prerelease of every operand triple.
func candidates(lower, upper bound, hasLower, hasUpper bool, cmps []comparator) []Version {
	var out []Version
	if hasLower {
		if lower.strict {
			out = append(out, successor(lower.v))
		} else {
			out = append(out, lower.v)
		}
		out = append(out, nextRelease(lower.v))
	} else {
		out = append(out, Version{})
	}
	if hasUpper && !upper.strict {
		out = append(out, upper.v)
	}
	for _, c := range cmps {
		if c.v.IsPrerelease() {
			out = append(out, Version{
				Major: c.v.Major, Minor: c.v.Minor, Patch: c.v.Patch,
				Pre: []string{"0"},
			})
		}
	}
	return out
}

// successor returns the smallest version strictly greater than v.
func successor(v Version) Version {
	if v.IsPrerelease() {
		pre := make([]string, len(v.Pre)+1)
		copy(pre, v.Pre)
		pre[len(v.Pre)] = "0"
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch, Pre: pre}
	}
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1, Pre: []string{"0"}}
}

// nextRelease returns the smallest release version strictly greater than v.
func nextRelease(v Version) Version {
	if v.IsPrerelease() {
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
	}
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}

// blame finds a constraint responsible for v not matching r.
func (r Range) blame(v Version) string {
	for _, g := range r.groups {
		if v.IsPrerelease() && !groupAllowsPrerelease(g, v) {
			return g[0].orig
		}
		for _, c := range g {
			if !c.match(v) {
				return c.orig
			}
		}
	}
	return r.orig
}
