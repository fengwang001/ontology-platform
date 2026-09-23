// Package hunk groups an edit script into context hunks.
package hunk

import "ontology/edit"

// Hunk is one contiguous group of operations with up to C context lines
// on each side. Start fields are the 1-based hunk header coordinates
// (zero-length ranges use the preceding-line convention, see DESIGN.md).
type Hunk struct {
	OldStart int
	OldLen   int
	NewStart int
	NewLen   int
	Ops      []edit.Op
}

// Group partitions ops into hunks with ctx context lines. Two changes
// separated by g equal lines merge iff g <= 2*ctx.
func Group(ops []edit.Op, ctx int) []Hunk {
	return nil
}
