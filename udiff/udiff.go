// Package udiff renders and parses unified diff text.
package udiff

import (
	"errors"

	"ontology/hunk"
	"ontology/lines"
)

// File names a patch side.
type File struct {
	Name string
}

// Hunk is a parsed/rendered hunk: coordinates plus the A and B side
// lines (Content plus original EOL terminators).
type Hunk struct {
	OldStart int
	OldLen   int
	NewStart int
	NewLen   int
	A        []lines.Line
	B        []lines.Line
	Inner    *hunk.Hunk
}

// Patch is one unified diff document.
type Patch struct {
	Old File
	New File
	Hunks []Hunk
}

// Limits bounds parsing. Zero means unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Error sentinels for the four distinguishable failure classes.
var (
	ErrFormat   = errors.New("udiff: malformed patch")
	ErrTooLarge = errors.New("udiff: patch exceeds configured limits")
)

// FormatError reports a parse failure with the 1-based line number in
// the patch text.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return "" }
func (e *FormatError) Unwrap() error { return ErrFormat }

// Diff builds a patch transforming a into b (maxDist < 0 = unlimited).
func Diff(a, b []byte, oldName, newName string, ctx, maxDist int) (*Patch, error) {
	return nil, nil
}

// Render writes unified diff text for p.
func Render(p *Patch) []byte { return nil }

// Parse reads unified diff text under the given limits.
func Parse(text []byte, lim Limits) (*Patch, error) { return nil, nil }
