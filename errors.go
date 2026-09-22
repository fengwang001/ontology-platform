package ontology

import "fmt"

// Kind classifies a computation failure so callers can branch on it
// with errors.As.
type Kind int

const (
	// KindLengthMismatch: Indices and Values have different lengths.
	KindLengthMismatch Kind = iota
	// KindNotSorted: Indices are not strictly ascending.
	KindNotSorted
	// KindNaN: a stored value is NaN.
	KindNaN
	// KindZeroNorm: cosine is undefined for a zero-norm vector.
	KindZeroNorm
	// KindNonFinite: the result would be Inf or NaN (e.g. inputs
	// containing infinities).
	KindNonFinite
)

// Error describes a validation or numerical failure. Vector is 0 for
// the first argument and 1 for the second; Position is the offending
// element offset within that vector. Both are -1 when not applicable.
type Error struct {
	Kind     Kind
	Vector   int
	Position int
	Detail   string
}

func (e *Error) Error() string {
	loc := ""
	if e.Vector >= 0 {
		loc = fmt.Sprintf("vector %d", e.Vector)
		if e.Position >= 0 {
			loc += fmt.Sprintf(" position %d", e.Position)
		}
		loc += ": "
	}
	return loc + e.Detail
}

func newError(kind Kind, vector, position int, detail string) *Error {
	return &Error{Kind: kind, Vector: vector, Position: position, Detail: detail}
}
