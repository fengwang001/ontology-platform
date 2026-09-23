// Package hunk groups a shortest edit script into context hunks.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one rendered body line: exactly one of Ctx/Del/Ins is meaningful.
type Item struct {
	Kind    byte // ' ', '-', '+'
	Line    lines.Line
	OldNoNL bool // for '+' lines: old final line had no newline
}

// Hunk is one context group with 1-based recorded ranges.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Body               []Item
}

// Build groups ops with C context lines; adjacent hunks with <= 2C unchanged
// lines between them are merged.
func Build(ops []edit.Op, nOld, nNew, c int) []Hunk { return nil }
