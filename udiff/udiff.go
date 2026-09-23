// Package udiff renders and parses unified-diff text for a single file pair.
package udiff

import "ontology/hunk"

// Limits bounds parser resource usage.
type Limits struct {
	MaxBytes int // total text bytes; <=0 means unlimited
	MaxHunks int // number of hunks; <=0 means unlimited
}

// Patch is a parsed/rendered single-file unified diff.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// SyntaxError is a malformed-patch error with 1-based text line number.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string { return "" }

// Render produces canonical unified-diff text.
func Render(p *Patch) string { return "" }

// Parse strictly parses text; header counts must match body exactly.
func Parse(text string, lim Limits) (*Patch, error) { return nil, nil }
