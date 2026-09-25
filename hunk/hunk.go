// Package hunk groups an edit script into contextual hunks.
package hunk

import "ontology/edit"

// Item is one rendered-ready entry of a hunk body.
type Item struct {
	Op edit.Op
}

// Hunk is one contiguous group of changes with context.
type Hunk struct {
	OldStart int // 1-based; for an empty old range, line before the point (0 allowed)
	OldCount int
	NewStart int
	NewCount int
	Items    []Item
}

// Build groups script into hunks with up to context lines of context each side.
func Build(script []edit.Step, context int) []Hunk {
	return nil
}
