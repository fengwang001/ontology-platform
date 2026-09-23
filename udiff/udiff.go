// Package udiff renders and parses unified diff text.
package udiff

import "ontology/hunk"

// Patch is a parsed or rendered unified diff between two paths.
type Patch struct {
	OldPath string
	NewPath string
	Hunks   []hunk.Hunk
}

// FormatError is a strict-parse failure carrying the 1-based text line.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return "" }

// Render produces unified diff bytes (headers, hunks, no-newline marks).
func Render(p Patch) []byte { return nil }

// Parse strictly parses unified diff text.
func Parse(data []byte) (Patch, error) { return Patch{}, nil }
