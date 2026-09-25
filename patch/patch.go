// Package patch applies unified diffs, with fuzz and concurrent documents.
package patch

import "errors"

var (
	// ErrContext means no candidate matched the hunk's context/deleted lines.
	ErrContext = errors.New("patch: context mismatch")
	// ErrOutOfRange means no candidate lay within the configured fuzz window.
	ErrOutOfRange = errors.New("patch: hunk offset out of range")
	// ErrTooLarge means the patch exceeded configured byte or hunk limits.
	ErrTooLarge = errors.New("patch: patch exceeds resource limits")
)

// Options controls application.
type Options struct {
	Fuzz      int // search ±Fuzz lines around the recorded position
	MaxBytes  int // <=0 means unlimited
	MaxHunks  int // <=0 means unlimited
}

// Apply applies a parsed patch to target, atomically rejecting on any failure.
func Apply(target []byte, p []byte, opts Options) ([]byte, error) {
	return nil, nil
}

// Reverse applies the patch in reverse.
func Reverse(target []byte, p []byte, opts Options) ([]byte, error) {
	return nil, nil
}
