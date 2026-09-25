package edit

import "ontology/lines"

type OpKind int

const (
	Equal OpKind = iota
	Delete
	Insert
)

type Op struct {
	Kind OpKind
	A, B lines.Line
	AI, BI int
}

type TooLargeError struct{}

func (TooLargeError) Error() string { return "difference exceeds edit distance limit" }

var Steps int

func Diff(a, b []lines.Line, maxDistance int) ([]Op, error) { return nil, nil }
