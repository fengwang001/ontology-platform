// Package edit computes a shortest edit script between two line sequences.
package edit

import "ontology/lines"

// Kind is an edit operation kind.
type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

// Op is one edit operation referencing one old/new line index.
type Op struct {
	Kind Kind
	Old  int
	New int
}

// ErrTooDifferent reports distance exceeding the configured limit.
var ErrTooDifferent = errDiff("edit: difference exceeds limit")

type errDiff string

func (e errDiff) Error() string { return string(e) }

// Script is a shortest edit script with distance metadata.
type Script struct {
	Ops       []Op
	Distance  int
	steps     int
	old, fresh []lines.Line
}

// Diff returns a shortest edit script; limit<0 means unlimited.
func Diff(a, b []lines.Line, limit int) (*Script, error) {
	return nil, nil
}

// Steps reports diagonal advances counted by the most recent Diff call.
func (s *Script) Steps() int { return s.steps }
