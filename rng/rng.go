// Package rng parses version constraints ("<op>version ..."), intersects
// them and decides emptiness purely from interval endpoints.
package rng

import (
	"errors"
	"fmt"
	"strings"

	"ontology/ver"
)

// ErrInvalidConstraint reports a constraint string that violates the
// grammar: one or more whitespace-separated "<op><version>" comparators
// with op in >=, <=, >, <, = (a bare version means "=").
var ErrInvalidConstraint = errors.New("rng: invalid constraint")

type op int

const (
	opLT op = iota
	opLE
	opGT
	opGE
	opEQ
)

type comparator struct {
	op op
	v  ver.Version
}

// Constraint is a conjunction of comparators, i.e. an interval with
// optionally open endpoints.
type Constraint struct {
	raw  string
	cmps []comparator
}

// Parse parses s into a Constraint.
func Parse(s string) (*Constraint, error) {
	toks := strings.Fields(s)
	if len(toks) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrInvalidConstraint, s)
	}
	c := &Constraint{raw: s}
	for _, t := range toks {
		cmp, err := parseComparator(t)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidConstraint, s)
		}
		c.cmps = append(c.cmps, cmp)
	}
	return c, nil
}

func parseComparator(t string) (comparator, error) {
	var cmp comparator
	rest := t
	for _, p := range []struct {
		prefix string
		op     op
	}{{"<=", opLE}, {">=", opGE}, {"<", opLT}, {">", opGT}, {"=", opEQ}} {
		if strings.HasPrefix(t, p.prefix) {
			cmp.op = p.op
			rest = t[len(p.prefix):]
			break
		}
	}
	if rest == t && cmp.op == opLT && !strings.HasPrefix(t, "<") {
		cmp.op = opEQ // bare version
	}
	v, err := ver.Parse(rest)
	if err != nil {
		return cmp, err
	}
	cmp.v = v
	return cmp, nil
}

// String returns the constraint text as parsed (joined for intersections).
func (c *Constraint) String() string { return c.raw }

// Contains reports whether v satisfies every comparator.
func (c *Constraint) Contains(v ver.Version) bool {
	for _, cmp := range c.cmps {
		r := v.Compare(cmp.v)
		switch cmp.op {
		case opLT:
			if r >= 0 {
				return false
			}
		case opLE:
			if r > 0 {
				return false
			}
		case opGT:
			if r <= 0 {
				return false
			}
		case opGE:
			if r < 0 {
				return false
			}
		case opEQ:
			if r != 0 {
				return false
			}
		}
	}
	return true
}

// Intersect returns the conjunction of c and o.
func (c *Constraint) Intersect(o *Constraint) *Constraint {
	return &Constraint{
		raw:  c.raw + " " + o.raw,
		cmps: append(append([]comparator{}, c.cmps...), o.cmps...),
	}
}

// bound is an interval endpoint: a version plus whether it is included.
type bound struct {
	v      ver.Version
	closed bool
	set    bool
}

// bounds computes the tightest lower and upper endpoint of the interval.
func (c *Constraint) bounds() (lo, hi bound) {
	for _, cmp := range c.cmps {
		switch cmp.op {
		case opGT:
			if !lo.set || cmp.v.Compare(lo.v) > 0 || (cmp.v.Compare(lo.v) == 0 && lo.closed) {
				lo = bound{cmp.v, false, true}
			}
		case opGE:
			if !lo.set || cmp.v.Compare(lo.v) > 0 || (cmp.v.Compare(lo.v) == 0 && lo.closed) {
				lo = bound{cmp.v, true, true}
			}
		case opLT:
			if !hi.set || cmp.v.Compare(hi.v) < 0 || (cmp.v.Compare(hi.v) == 0 && hi.closed) {
				hi = bound{cmp.v, false, true}
			}
		case opLE:
			if !hi.set || cmp.v.Compare(hi.v) < 0 || (cmp.v.Compare(hi.v) == 0 && hi.closed) {
				hi = bound{cmp.v, true, true}
			}
		case opEQ:
			if !lo.set || cmp.v.Compare(lo.v) > 0 || (cmp.v.Compare(lo.v) == 0 && lo.closed) {
				lo = bound{cmp.v, true, true}
			}
			if !hi.set || cmp.v.Compare(hi.v) < 0 || (cmp.v.Compare(hi.v) == 0 && hi.closed) {
				hi = bound{cmp.v, true, true}
			}
		}
	}
	return lo, hi
}

// IsEmpty reports whether the interval contains no version at all,
// decided purely from endpoints: empty iff lo > hi, or lo == hi and
// the interval is not closed at both ends.
func (c *Constraint) IsEmpty() bool {
	lo, hi := c.bounds()
	if !lo.set || !hi.set {
		return false
	}
	switch lo.v.Compare(hi.v) {
	case 1:
		return true
	case 0:
		return !(lo.closed && hi.closed)
	}
	return false
}
