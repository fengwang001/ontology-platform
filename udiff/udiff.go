// Package udiff renders and parses unified diff text.
package udiff

import "ontology/edit"

// Line is one hunk body line. NoNL marks "\ No newline at end of file"
// attached to the preceding content line.
type Line struct {
	Kind edit.Kind
	Text string
	NoNL bool
}

// Hunk is a parsed or rendered hunk with 1-based start coordinates.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines               []Line
}

// File is one ---/+++ document pair.
type File struct {
	OldName, NewName string
	Hunks            []*Hunk
}

// ParseError names the patch-text line number of a format error.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return "" }

// Render serializes one file diff.
func Render(oldName, newName string, hs []*Hunk) []byte { return nil }

// Parse decodes one file diff, enforcing exact declared counts.
func Parse(data []byte, maxHunks, maxBytes int) (*File, error) { return nil, nil }
