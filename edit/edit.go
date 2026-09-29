package edit

import (
	"errors"

	"ontology/lines"
)

type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

type Op struct {
	Kind Kind
	A    lines.Line
	B    lines.Line
}

var ErrTooDifferent = errors.New("edit: difference exceeds limit")

var steps int64

func LastSteps() int64 { return steps }

func Diff(a, b []lines.Line, maxD int) ([]Op, error) {
	return nil, nil
}
