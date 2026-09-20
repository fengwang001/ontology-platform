package semver

import "strings"

// op is a comparison operator of a single comparator.
type op int

const (
	opEQ op = iota // =
	opLT           // <
	opLE           // <=
	opGT           // >
	opGE           // >=
)

// comparator is a single (operator, version) test.
type comparator struct {
	op op
	v  Version
}

func (c comparator) match(v Version) bool {
	switch c.op {
	case opEQ:
		return Compare(v, c.v) == 0
	case opLT:
		return Compare(v, c.v) < 0
	case opLE:
		return Compare(v, c.v) <= 0
	case opGT:
		return Compare(v, c.v) > 0
	case opGE:
		return Compare(v, c.v) >= 0
	}
	return false
}

// constraint is one space-separated token of a range, kept as written.
// Caret and tilde forms expand to two comparators but remember their
// original spelling for error messages.
type constraint struct {
	orig string
	cmps []comparator
}

// match reports whether v satisfies every comparator of the constraint.
func (c constraint) match(v Version) bool {
	for _, cmp := range c.cmps {
		if !cmp.match(v) {
			return false
		}
	}
	return true
}

// parseConstraint parses one constraint token such as ">=1.2.3", "^0.2.3"
// or "~1.2.3".
func parseConstraint(tok string) (constraint, error) {
	bad := func() (constraint, error) {
		return constraint{}, &ParseError{Kind: "range", Input: tok, Err: ErrInvalidRange}
	}
	c := constraint{orig: tok}
	switch {
	case strings.HasPrefix(tok, ">="):
		return c.withVer(tok[2:], opGE)
	case strings.HasPrefix(tok, "<="):
		return c.withVer(tok[2:], opLE)
	case strings.HasPrefix(tok, ">"):
		return c.withVer(tok[1:], opGT)
	case strings.HasPrefix(tok, "<"):
		return c.withVer(tok[1:], opLT)
	case strings.HasPrefix(tok, "="):
		return c.withVer(tok[1:], opEQ)
	case strings.HasPrefix(tok, "^"):
		v, err := Parse(tok[1:])
		if err != nil {
			return bad()
		}
		c.cmps = []comparator{{opGE, v}, {opLT, caretUpper(v)}}
		return c, nil
	case strings.HasPrefix(tok, "~"):
		v, err := Parse(tok[1:])
		if err != nil {
			return bad()
		}
		c.cmps = []comparator{{opGE, v}, {opLT, Version{Major: v.Major, Minor: v.Minor + 1}}}
		return c, nil
	}
	return bad()
}

func (c constraint) withVer(s string, o op) (constraint, error) {
	v, err := Parse(s)
	if err != nil {
		return constraint{}, &ParseError{Kind: "range", Input: c.orig, Err: ErrInvalidRange}
	}
	c.cmps = []comparator{{o, v}}
	return c, nil
}

// caretUpper computes the exclusive upper bound for ^v: the next
// incompatible release. Under 0.x every level is a breaking change.
func caretUpper(v Version) Version {
	switch {
	case v.Major > 0:
		return Version{Major: v.Major + 1}
	case v.Minor > 0:
		return Version{Minor: v.Minor + 1}
	default:
		return Version{Patch: v.Patch + 1}
	}
}
