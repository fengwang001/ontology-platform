package edit

import (
	"errors"

	"ontology/lines"
)

// ErrTooDifferent is returned when the edit distance exceeds MaxDist.
var ErrTooDifferent = errors.New("edit: difference exceeds limit")

// OpKind identifies an edit operation.
type OpKind int

const (
	Equal OpKind = iota
	Delete
	Insert
)

// Op is one operation of the shortest edit script.
type Op struct {
	Kind OpKind
	A    lines.Line // Equal/Delete: old line
	B    lines.Line // Equal/Insert: new line
}

// Script is a shortest edit script plus the diagonal-step counter.
type Script struct {
	Ops []Op
	// Steps counts diagonal/snake comparisons advanced during the last diff.
	Steps int
}

// Dist is deletions plus insertions.
func (s *Script) Dist() int { return 0 }

// Options tunes Diff. MaxDist<=0 means unlimited.
type Options struct {
	MaxDist int
}

// Diff computes a shortest edit script between a and b.
func Diff(a, b []lines.Line, opts Options) (*Script, error) {
	return nil, nil
}
