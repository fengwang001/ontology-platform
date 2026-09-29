package udiff

import (
	"errors"

	"ontology/hunk"
)

// Sentinel errors; FormatError carries the 1-based patch text line number.
var (
	ErrFormat = errors.New("udiff: malformed patch")
	ErrLimits = errors.New("udiff: resource limit exceeded")
)

// FormatError reports a parse error and its line in the patch text.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return ErrFormat.Error() + ": " + e.Msg }
func (e *FormatError) Unwrap() error { return ErrFormat }

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName string
	NewName string
	Hunks   []*hunk.Hunk
}

// Limits bounds parsing. Zero means unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Render produces unified diff text.
func Render(p *Patch) string { return "" }

// Parse parses unified diff text strictly.
func Parse(text string, lim Limits) (*Patch, error) {
	return nil, nil
}
