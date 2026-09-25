package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops []edit.Op
}

func Build(a, b []lines.Line, ops []edit.Op, context int) []Hunk { return nil }
