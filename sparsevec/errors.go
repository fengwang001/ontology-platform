package sparsevec

import (
	"errors"
	"fmt"
)

// Sentinel errors, matchable with errors.Is against any *Error of the kind.
var (
	ErrNotSorted = errors.New("sparsevec: indices not strictly ascending")
	ErrNaNValue  = errors.New("sparsevec: NaN value")
	ErrZeroNorm  = errors.New("sparsevec: cosine undefined for zero-norm vector")
	ErrNonFinite = errors.New("sparsevec: non-finite result")
)

// Kind classifies a computation failure.
type Kind int

const (
	KindNotSorted Kind = iota
	KindNaN
	KindZeroNorm
	KindNonFinite
)

// Error describes a failure with enough location context to be actionable.
type Error struct {
	Kind Kind
	// Vector is 0 for the first operand, 1 for the second, -1 when N/A.
	Vector int
	// Position is the element offset inside the offending vector, -1 when N/A.
	Position int
	Detail string
}

func (e *Error) Error() string {
	loc := ""
	if e.Vector >= 0 {
		loc = fmt.Sprintf(" (vector %d, position %d)", e.Vector, e.Position)
	}
	return e.sentinel().Error() + loc + ": " + e.Detail
}

func (e *Error) sentinel() error {
	switch e.Kind {
	case KindNotSorted:
		return ErrNotSorted
	case KindNaN:
		return ErrNaNValue
	case KindZeroNorm:
		return ErrZeroNorm
	default:
		return ErrNonFinite
	}
}

// Is lets errors.Is(err, ErrNotSorted) etc. match by kind.
func (e *Error) Is(target error) bool { return target == e.sentinel() }

func notSortedErr(vec, pos int, prev, cur uint32) *Error {
	return &Error{
		Kind:     KindNotSorted,
		Vector:   vec,
		Position: pos,
		Detail:   fmt.Sprintf("index %d at position %d not greater than previous index %d", cur, pos, prev),
	}
}

func nanErr(vec, pos int) *Error {
	return &Error{
		Kind:     KindNaN,
		Vector:   vec,
		Position: pos,
		Detail:   "value is NaN",
	}
}

func zeroNormErr(vec int) *Error {
	return &Error{
		Kind:   KindZeroNorm,
		Vector: vec,
		Detail: "vector has zero norm (empty or all-zero)",
	}
}

func nonFiniteErr(what string, got float64) *Error {
	return &Error{
		Kind:   KindNonFinite,
		Vector: -1,
		Detail: fmt.Sprintf("%s evaluated to %v", what, got),
	}
}
