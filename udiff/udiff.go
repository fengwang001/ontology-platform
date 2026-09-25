// Package udiff renders and parses unified diff text.
package udiff

import (
	"errors"

	"ontology/hunk"
)

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
}

// Limits bound parsing resources.
type Limits struct {
	MaxBytes int // <=0 means unlimited
	MaxHunks int // <=0 means unlimited
}

// FormatError is a strict parse error located at a patch-text line.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return "" }

// ErrTooLarge reports a resource limit violation.
var ErrTooLarge = errors.New("udiff: patch exceeds configured size or hunk limit")

// Render produces unified diff text from a structured patch.
func Render(p *Patch) []byte { return nil }

// Parse strictly parses unified diff text under the given limits.
func Parse(data []byte, lim Limits) (*Patch, error) { return nil, nil }
