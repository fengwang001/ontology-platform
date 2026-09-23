// Package edit computes shortest edit scripts with forward Myers O(ND).
package edit

import (
	"errors"

	"ontology/lines"
)

// Op is one edit operation.
type Op struct {
	Kind Kind
	A    lines.Line // old line for Del/Eq
	B    lines.Line // new line for Ins/Eq
}

// Kind is the edit operation kind.
type Kind uint8

const (
	Eq  Kind = iota // unchanged line
	Del             // removed from a
	Ins             // inserted from b
)

// ErrTooDifferent is returned when edit distance exceeds MaxD.
var ErrTooDifferent = errors.New("edit: files differ too much")

// Options bounds the search. MaxD<=0 means unlimited.
type Options struct{ MaxD int }

// Result is a shortest edit script plus diagnostics.
type Result struct {
	Script []Op
	D      int // number of del+ins ops
	Steps  int // snake advance steps including per-line compares
}

// Diff computes a shortest (delete-first on ties) script from a to b.
func Diff(a, b []lines.Line, opts Options) (Result, error) {
	return Result{}, nil
}
