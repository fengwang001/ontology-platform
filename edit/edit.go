// Package edit computes a shortest edit script between two line sequences.
package edit

import "errors"

// Op kinds.
const (
	Equal  uint8 = 0
	Delete uint8 = 1
	Insert uint8 = 2
)

// ErrTooDifferent reports that the edit distance exceeds the configured cap.
var ErrTooDifferent = errors.New("edit: edit distance exceeds limit")

// Op is one edit operation. A holds the old line (Equal/Delete), B the new
// line (Equal/Insert), each with its original terminator.
type Op struct {
	Kind uint8
	A, B []byte
}

// Option configures a Differ.
type Option func(*Differ)

// WithMaxDist caps the edit distance (deletions+insertions); <=0 is unlimited.
func WithMaxDist(d int) Option {
	return func(z *Differ) { z.maxDist = d }
}

// Differ runs Myers O(ND) shortest-edit-script diffs.
type Differ struct {
	maxDist int
	steps   int64
}

// New builds a Differ.
func New(opts ...Option) *Differ {
	z := &Differ{}
	for _, o := range opts {
		o(z)
	}
	return z
}

// Steps returns diagonal advance steps (including per-line snake compares)
// recorded by the most recent Diff call.
func (z *Differ) Steps() int64 { return z.steps }

// Diff returns a shortest edit script from a to b.
func (z *Differ) Diff(a, b [][]byte) ([]Op, error) {
	return nil, nil
}
