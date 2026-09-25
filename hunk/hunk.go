// Package hunk groups an edit script into contextual unified hunks.
package hunk

import "ontology/edit"

// Hunk is one contiguous group of edit operations (contains changes).
type Hunk struct {
	OldStart int // 1-based start in the old file; 0 when old count is 0
	OldLen   int
	NewStart int // 1-based start in the new file; 0 when new count is 0
	NewLen   int
	Ops      []edit.Op
}

// Build groups ops into hunks with at most C lines of context each side,
// merging neighboring hunks separated by at most 2*C unchanged lines.
func Build(ops []edit.Op, c int) []Hunk { return nil }
