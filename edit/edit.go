// Package edit computes shortest edit scripts with Myers' O(ND) algorithm.
package edit

import "ontology/lines"

// OpKind identifies an edit operation.
type OpKind int

const (
	Equal OpKind = iota
	Delete
	Insert
)

// Op is one aligned operation between the two line sequences.
type Op struct {
	Kind OpKind
	A    lines.Line
	B    lines.Line
}

// ErrTooDifferent is returned when the distance exceeds MaxDistance.
var ErrTooDifferent = errors.New("edit: difference exceeds configured limit")

// Options controls shortest-script computation.
type Options struct {
	MaxDistance int
}

// Steps returns the number of diagonal/snake comparisons from the latest Diff.
func Steps() int64 { return 0 }

// Diff returns a shortest edit script from a to b.
func Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	return nil, nil
}
