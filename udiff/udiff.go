// Package udiff renders and parses unified-format diff text.
package udiff

import (
	"errors"

	"ontology/hunk"
)

// Limits bounds Parse: 0 means unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	From, To string
	Hunks    []hunk.Hunk
}

// ErrFormat is the sentinel for malformed patch text.
var ErrFormat = errors.New("udiff: malformed patch")

// FormatError carries the 1-based line number in the patch text.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return ErrFormat.Error() + ": " + e.Msg }
func (e *FormatError) Unwrap() error { return ErrFormat }

// Render writes ops (context C) as unified diff bytes.
func Render(from, to string, c int, ops []edit.Op) []byte { return nil }

// Parse reads unified diff text under the given limits.
func Parse(b []byte, lim Limits) (*Patch, error) { return nil, nil }
