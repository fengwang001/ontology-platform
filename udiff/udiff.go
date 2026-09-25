// Package udiff renders and parses unified diff text, preserving original
// line terminators and "\ No newline at end of file" markers.
package udiff

import (
	"errors"

	"ontology/hunk"
	"ontology/lines"
)

// ErrFormat is the sentinel for a malformed patch. ParseError wraps it and
// reports the 1-based line number in the patch text.
var ErrFormat = errors.New("udiff: malformed patch")

// ParseError describes a format error at patch-text line Line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return "" }
func (e *ParseError) Unwrap() error { return ErrFormat }

// Patch is a parsed unified diff document.
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
}

// Diff builds hunks transforming a into b with ctx context lines.
// maxEdits<=0 means unlimited edit distance.
func Diff(a, b []byte, ctx, maxEdits int) (*Patch, error) {
	return nil, nil
}

// Render serializes p into unified-diff bytes.
func Render(p *Patch) []byte {
	return nil
}

// Parse decodes unified-diff text, enforcing exact hunk line counts.
// maxBytes<=0 and maxHunks<=0 mean unlimited.
func Parse(text []byte, maxBytes, maxHunks int) (*Patch, error) {
	return nil, nil
}

// Reverse returns the inverse patch (b -> a).
func Reverse(p *Patch) *Patch {
	return nil
}

var _ = lines.Line{}
