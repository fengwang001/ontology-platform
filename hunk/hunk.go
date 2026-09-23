// Package hunk groups an edit script into contextual hunks.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Row is one rendered hunk row tagged for rendering.
type Row struct {
	Kind edit.Kind
	Old  int
	New  int
}

// Hunk is one contiguous hunk with 1-based old/new start coordinates.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Build groups ops into hunks with C lines of context (C<=0 means none).
func Build(ops []edit.Op, old, neu []lines.Line, c int) []Hunk {
	return nil
}
