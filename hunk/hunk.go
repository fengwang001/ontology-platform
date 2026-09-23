// Package hunk groups an edit script into context hunks.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one rendered line inside a hunk.
type Item struct {
	Kind edit.Kind
	A    lines.Line
	B    lines.Line
}

// Hunk is a contiguous group with old/new positions (1-based counts,
// zero-count position rules per unified diff).
type Hunk struct {
	OldStart int // a in -a,b
	OldCount int // b
	NewStart int // c in +c,d
	NewCount int // d
	Items    []Item
}

// Group splits ops into hunks using C context lines, merging adjacent
// hunks separated by at most 2*C unchanged lines.
func Group(ops []edit.Op, c int) []Hunk {
	return nil
}
