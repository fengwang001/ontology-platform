package edit

import "ontology/lines"

// Op is one edit operation on aligned line sequences.
type Op struct {
	Kind byte   // ' ' equal, '-' delete, '+' insert
	Old  string // old line text without terminator (for ' '/'-')
	New  string // new line text without terminator (for ' '/'+' )
}

// Differ computes shortest edit scripts with a configurable distance cap.
type Differ struct {
	MaxDist int // <=0 means unlimited
	Steps   int // non-exported-semantics counter of diagonal work
}

// ErrTooDifferent is returned when edit distance exceeds MaxDist.
var ErrTooDifferent = errDiff("differences exceed configured edit-distance limit")

type errDiff string

func (e errDiff) Error() string { return string(e) }

// Diff returns the shortest op sequence from a to b.
func (d *Differ) Diff(a, b []lines.Line) ([]Op, error) { return nil, nil }
