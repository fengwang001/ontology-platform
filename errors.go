package sparse

import "fmt"

// Kind classifies a computation failure so callers can switch on it.
type Kind int

const (
	// ErrNotSorted: element indices are not strictly ascending.
	ErrNotSorted Kind = iota
	// ErrNaN: an element value is NaN.
	ErrNaN
	// ErrZeroNorm: cosine is undefined because a vector has zero norm.
	ErrZeroNorm
	// ErrNonFinite: the result would be Inf or NaN (e.g. Inf inputs).
	ErrNonFinite
)

// Error is the single error type returned by this package. Use errors.As
// to inspect Kind, Vector and Position.
type Error struct {
	Kind     Kind
	Vector   string // "left" or "right"; empty when not vector-specific
	Position int    // element position within Vector; -1 when not applicable
	msg      string
}

func (e *Error) Error() string { return e.msg }

func sortErr(vec string, pos int, prev, cur uint32) *Error {
	return &Error{
		Kind:     ErrNotSorted,
		Vector:   vec,
		Position: pos,
		msg: fmt.Sprintf("sparse: %s vector element %d: index %d not strictly "+
			"greater than previous index %d", vec, pos, cur, prev),
	}
}

func nanErr(vec string, pos int) *Error {
	return &Error{
		Kind:     ErrNaN,
		Vector:   vec,
		Position: pos,
		msg:      fmt.Sprintf("sparse: %s vector element %d: value is NaN", vec, pos),
	}
}

func zeroNormErr(vec string) *Error {
	return &Error{
		Kind:     ErrZeroNorm,
		Vector:   vec,
		Position: -1,
		msg:      fmt.Sprintf("sparse: cosine undefined: %s vector has zero norm", vec),
	}
}

func nonFiniteErr(what string, got float64) *Error {
	return &Error{
		Kind:     ErrNonFinite,
		Position: -1,
		msg:      fmt.Sprintf("sparse: %s is not finite (got %v)", what, got),
	}
}
