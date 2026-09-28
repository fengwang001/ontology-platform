package hunk

import "ontology/edit"

type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

func Build(ops []edit.Op, ctx int) []Hunk { return nil }
