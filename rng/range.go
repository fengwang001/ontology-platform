// Package rng parses version constraints and intersects version intervals.
package rng

import (
	"errors"
	"strings"

	"ontology/ver"
)

// ErrSyntax is returned for a malformed constraint string.
var ErrSyntax = errors.New("rng: invalid constraint syntax")

type bound struct {
	v         ver.Version
	inclusive bool
}

// Range is a conjunction interval derived from one constraint string.
type Range struct {
	lo, hi *bound // nil means unbounded
	empty  bool
}

// All is the unconstrained interval.
func All() Range { return Range{} }

// Low returns the lower bound: present=false means unbounded.
func (r Range) Low() (v ver.Version, present, inclusive bool) {
	if r.lo == nil {
		return ver.Version{}, false, false
	}
	return r.lo.v, true, r.lo.inclusive
}

// High returns the upper bound: present=false means unbounded.
func (r Range) High() (v ver.Version, present, inclusive bool) {
	if r.hi == nil {
		return ver.Version{}, false, false
	}
	return r.hi.v, true, r.hi.inclusive
}

// Parse parses constraints like ">=1.2.0 <2.0.0".
func Parse(s string) (Range, error) {
	r := Range{}
	toks := strings.Fields(s)
	if len(toks) == 0 {
		return Range{}, ErrSyntax
	}
	for _, tok := range toks {
		var op string
		switch {
		case strings.HasPrefix(tok, ">="):
			op = ">="
		case strings.HasPrefix(tok, "<="):
			op = "<="
		case strings.HasPrefix(tok, ">"):
			op = ">"
		case strings.HasPrefix(tok, "<"):
			op = "<"
		case strings.HasPrefix(tok, "="):
			op = "="
		default:
			return Range{}, ErrSyntax
		}
		v, err := ver.Parse(tok[len(op):])
		if err != nil {
			return Range{}, ErrSyntax
		}
		b := &bound{v: v, inclusive: op != ">" && op != "<"}
		switch op {
		case ">=", ">":
			if tighterLow(b, r.lo) {
				r.lo = b
			}
		case "<=", "<":
			if tighterHigh(b, r.hi) {
				r.hi = b
			}
		case "=":
			r.lo = b
			r.hi = &bound{v: v, inclusive: true}
		}
		if r.isEmptyInternal() {
			r.empty = true
			return r, nil
		}
	}
	return r, nil
}

func tighterLow(a, cur *bound) bool {
	if cur == nil {
		return true
	}
	c := ver.Compare(a.v, cur.v)
	return c > 0 || c == 0 && !a.inclusive && cur.inclusive
}

func tighterHigh(a, cur *bound) bool {
	if cur == nil {
		return true
	}
	c := ver.Compare(a.v, cur.v)
	return c < 0 || c == 0 && !a.inclusive && cur.inclusive
}

func (r Range) isEmptyInternal() bool {
	if r.lo == nil || r.hi == nil {
		return false
	}
	c := ver.Compare(r.lo.v, r.hi.v)
	return c > 0 || c == 0 && !(r.lo.inclusive && r.hi.inclusive)
}

// Intersect returns the intersection of two ranges.
func Intersect(a, b Range) Range {
	if a.empty || b.empty {
		return Range{empty: true}
	}
	r := Range{lo: pickLow(a.lo, b.lo), hi: pickHigh(a.hi, b.hi)}
	r.empty = r.isEmptyInternal()
	return r
}

func pickLow(a, b *bound) *bound {
	if a == nil {
		return b
	}
	if b == nil || tighterLow(a, b) {
		return a
	}
	return b
}

func pickHigh(a, b *bound) *bound {
	if a == nil {
		return b
	}
	if b == nil || tighterHigh(a, b) {
		return a
	}
	return b
}

// IsEmpty reports whether the interval contains no version point at all.
func (r Range) IsEmpty() bool { return r.empty || r.isEmptyInternal() }

// Contains reports whether v belongs to the interval.
func (r Range) Contains(v ver.Version) bool {
	if r.IsEmpty() {
		return false
	}
	if r.lo != nil {
		c := ver.Compare(v, r.lo.v)
		if c < 0 || c == 0 && !r.lo.inclusive {
			return false
		}
	}
	if r.hi != nil {
		c := ver.Compare(v, r.hi.v)
		if c > 0 || c == 0 && !r.hi.inclusive {
			return false
		}
	}
	return true
}

// String renders the canonical form.
func (r Range) String() string {
	if r.empty {
		return "<empty>"
	}
	var parts []string
	if r.lo != nil {
		op := ">"
		if r.lo.inclusive {
			op = ">="
		}
		parts = append(parts, op+r.lo.v.String())
	}
	if r.hi != nil {
		op := "<"
		if r.hi.inclusive {
			op = "<="
		}
		parts = append(parts, op+r.hi.v.String())
	}
	return strings.Join(parts, " ")
}
