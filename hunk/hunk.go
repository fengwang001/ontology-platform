// Package hunk groups a shortest edit script into unified-diff hunks with
// C context lines, GNU -U C compatible merge rules.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Row is one emitted line inside a hunk. A is set for Equal/Delete, B for Equal/Insert.
type Row struct {
	K       edit.Kind
	A, B    lines.Line
}

// Hunk is one contiguous @@ block with 1-based old/new start lines.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Build groups ops into hunks with C context lines.
func Build(ops []edit.Op, C int) []Hunk { return nil }
