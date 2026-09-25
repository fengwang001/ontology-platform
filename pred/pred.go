// Package pred defines the conjunctive predicate language, integer interval
// semantics, containment and residual-predicate computation.
package pred

import (
	"errors"
	"math"
)

// ErrInvalidAtom is the "谓词列非法" sentinel: unknown column, repeated atom,
// or a non-equality operator on the region column.
var ErrInvalidAtom = errors.New("pred: invalid predicate atom")

const (
	Region = "region"
	Amount = "amount"
	Year   = "year"
)

// Op enumerates comparisons allowed for integer columns.
type Op int

const (
	Eq Op = iota // =
	Lt           // <
	Le           // <=
	Gt           // >
	Ge           // >=
)

// Atom is one integer comparison; Pred is a conjunction, one atom/column max.
type Atom struct {
	Op  Op
	Val int
}

type Pred struct {
	Region *string
	Amount *Atom
	Year   *Atom
}

// I builds an integer atom.
func I(op Op, v int) *Atom { return &Atom{op, v} }

// RawAtom is a column-tagged constructor input; Build rejects malformed ones.
type RawAtom struct {
	Col string
	Op  Op
	Str string // region value when Col == region
	Val int    // integer value otherwise
}

// Build constructs a Pred from raw atoms.
func Build(ra ...RawAtom) (Pred, error) {
	var p Pred
	for _, a := range ra {
		switch a.Col {
		case Region:
			if a.Op != Eq || p.Region != nil {
				return Pred{}, ErrInvalidAtom
			}
			s := a.Str
			p.Region = &s
		case Amount:
			if p.Amount != nil {
				return Pred{}, ErrInvalidAtom
			}
			p.Amount = &Atom{a.Op, a.Val}
		case Year:
			if p.Year != nil {
				return Pred{}, ErrInvalidAtom
			}
			p.Year = &Atom{a.Op, a.Val}
		default:
			return Pred{}, ErrInvalidAtom
		}
	}
	return p, nil
}

// ival is a closed integer interval; math extrema mark infinite ends.
type ival struct{ lo, hi int }

func iv(a *Atom) ival {
	if a == nil {
		return ival{math.MinInt, math.MaxInt}
	}
	switch a.Op {
	case Eq:
		return ival{a.Val, a.Val}
	case Lt:
		return ival{math.MinInt, a.Val - 1}
	case Le:
		return ival{math.MinInt, a.Val}
	case Gt:
		return ival{a.Val + 1, math.MaxInt}
	default: // Ge
		return ival{a.Val, math.MaxInt}
	}
}

func (i ival) sub(o ival) bool { return i.lo >= o.lo && i.hi <= o.hi }

// Contains reports P ⊆ V column by column (intervals / singleton sets).
func Contains(sub, super Pred) bool {
	regOK := super.Region == nil || (sub.Region != nil && *sub.Region == *super.Region)
	return regOK && iv(sub.Amount).sub(iv(super.Amount)) && iv(sub.Year).sub(iv(super.Year))
}

func residAtom(p, v *Atom) *Atom {
	if p == nil || (v != nil && iv(p) == iv(v)) {
		return nil // absent, or already implied by the view predicate
	}
	c := *p
	return &c // strictly narrower: re-apply P's own atom
}

// Residual returns the atoms of p not implied by vp. The caller must have
// established Contains(p, vp); then vp ∧ Residual(p,vp) ≡ p on base rows.
func Residual(p, vp Pred) Pred {
	r := Pred{Amount: residAtom(p.Amount, vp.Amount), Year: residAtom(p.Year, vp.Year)}
	if p.Region != nil && (vp.Region == nil || *p.Region != *vp.Region) {
		s := *p.Region
		r.Region = &s
	}
	return r
}

func atomMatch(a *Atom, v int) bool {
	switch a.Op {
	case Eq:
		return v == a.Val
	case Lt:
		return v < a.Val
	case Le:
		return v <= a.Val
	case Gt:
		return v > a.Val
	default:
		return v >= a.Val
	}
}

// Match evaluates the conjunction against one base-table row.
func (p Pred) Match(region string, amount, year int) bool {
	return (p.Region == nil || region == *p.Region) &&
		(p.Amount == nil || atomMatch(p.Amount, amount)) &&
		(p.Year == nil || atomMatch(p.Year, year))
}
