package edit

import (
	"errors"

	"ontology/lines"
)

var ErrTooDifferent = errors.New("edit distance exceeds limit")

type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

type Op struct {
	Kind Kind
	Line lines.Line
}

type Script struct {
	Ops []Op
}

func (s Script) Distance() int { return 0 }

func Diff(a, b []lines.Line, maxD int) (Script, int, error) {
	return Script{}, 0, nil
}
