// Package patch applies parsed unified diffs, including fuzz offsets,
// atomic rejection, reverse application and concurrent multi-doc storage.
package patch

import "ontology/udiff"

// Reason classifies an application failure.
type Reason int

const (
	ReasonFormat     Reason = iota // patch itself is malformed
	ReasonContext                  // context/deleted lines do not match
	ReasonOutOfRange               // no match within the fuzz window
)

// Error identifies a failing hunk (1-based) and the failure category.
type Error struct {
	Hunk   int
	Reason Reason
}

func (e *Error) Error() string { return "" }

// Options configures application.
type Options struct {
	Fuzz     int // search recorded position ± this many lines
	MaxBytes int
	MaxHunks int
}

// Render is a convenience: diff a into b and render unified text.
func Render(a, b []byte, c, maxD int) []byte { return nil }

// Apply atomically applies text p to a; on failure a is returned unchanged.
func Apply(a []byte, p []byte, o Options) ([]byte, error) { return nil, nil }

// Reverse returns a patch whose direction is swapped (old<->new).
func Reverse(p *udiff.Patch) *udiff.Patch { return nil }
