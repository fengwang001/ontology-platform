// Package hunk groups an edit script into unified-diff hunks.
package hunk

import "ontology/edit"

// Hunk is one contiguous section of an edit script with context.
// AO/BO are 0-based starting line indices in the old/new files; an empty
// section points at the slice position (line before the insertion point).
type Hunk struct {
	AO, BO int
	Ops    []edit.Op
}

// Groups partitions ops into hunks using C context lines, merging neighbouring
// hunks whenever the gap of unchanged lines is at most 2*C.
func Groups(ops []edit.Op, c int) []Hunk {
	return nil
}
