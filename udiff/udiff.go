// Package udiff renders hunks to unified-diff text and parses it back strictly.
package udiff

import (
	"ontology/hunk"
	"ontology/lines"
)

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Limits guards parser resources.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// FmtError is a strict parse error carrying the 1-based patch text line.
type FmtError struct{ Line int; Msg string }

func (e *FmtError) Error() string { return "udiff: format error" }

// Render produces unified-diff text from a line pair and context C.
func Render(a, b []byte, C int) []byte { return nil }

// Parse strictly parses unified-diff text under the given resource limits.
func Parse(data []byte, lim Limits) (*Patch, error) { return nil, nil }

// Bytes is a convenience alias retained for symmetry with lines users.
func Bytes(ls []lines.Line) []byte { return lines.Join(ls) }
