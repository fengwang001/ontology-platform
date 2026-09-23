// Package edit computes a shortest edit script between two line
// sequences with Myers' O(ND) algorithm and a distance cap.
package edit

import "ontology/lines"

// Kind classifies a script operation.
type Kind uint8

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one script step. A is the old-side line (Delete/Equal),
// B is the new-side line (Insert/Equal).
type Op struct {
	Kind Kind
	A    lines.Line
	B    lines.Line
}

// ErrTooDifferent is returned when edit distance exceeds MaxD.
var ErrTooDifferent = errors.New("edit: differences exceed maximum edit distance")

// Options configures Diff.
type Options struct {
	MaxD int // <= 0 means unlimited
}

// Counter returns diagonal forward steps (per-line comparisons, including
// the failing one) counted by the most recent Diff in this process.
func Counter() int64 { return 0 }

// Diff returns a shortest script transforming a into b.
func Diff(a, b []lines.Line, opts Options) ([]Op, error) {
	return nil, nil
}
