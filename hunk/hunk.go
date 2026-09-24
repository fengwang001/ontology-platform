package hunk

import "ontology/edit"

type Hunk struct{ Ops []edit.Op }

func Group(s edit.Script, ctx int) []Hunk { return nil }
