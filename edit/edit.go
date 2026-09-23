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
	Line lines.Line
}

type Script struct {
	Ops      []Op
	Steps    int
	Distance int
}

type TooLargeError struct{}

func (TooLargeError) Error() string { return "edit distance exceeds limit" }

func Diff(a, b []lines.Line, limit int) (Script, error) {
	return Script{}, nil
}
