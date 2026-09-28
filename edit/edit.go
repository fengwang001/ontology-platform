package edit

import "ontology/lines"

type Op struct {
	Kind  int
	Line  lines.Line
}

var ErrTooLarge = errSentinel("edit: difference exceeds max edit distance")

func Steps() int { return steps }

func Diff(a, b []lines.Line, maxD int) ([]Op, error) { return nil, nil }

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
