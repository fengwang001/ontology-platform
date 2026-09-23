// Package udiff renders and parses unified diff text.
package udiff

import (
	"errors"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// ErrMalformed signals an unparsable patch; *MalformedError carries the line number.
var ErrMalformed = errors.New("udiff: malformed patch")

// MalformedError wraps ErrMalformed with the 1-based line in the patch text.
type MalformedError struct {
	Line int
	Msg  string
}

func (e *MalformedError) Error() string { return "" }
func (e *MalformedError) Unwrap() error { return ErrMalformed }

// Limit caps parsed resources; zero means unlimited.
type Limit struct {
	MaxBytes int
	MaxHunks int
}

// Patch is a parsed unified diff.
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.H
}

// Diff builds the shortest script and groups it with context C.
func Diff(a, b []byte, oldName, newName string, c int, maxD int) (Patch, error) {
	return Patch{}, nil
}

// Render prints the unified diff text.
func Render(p Patch) []byte {
	return nil
}

// Parse strictly parses unified diff text under the given limits.
func Parse(text []byte, lim Limit) (Patch, error) {
	return Patch{}, nil
}

var _ = lines.Split
var _ = edit.Eq
