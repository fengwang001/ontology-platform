// Package match implements a hash-chain longest-match finder over a window.
package match

import "ontology/window"

var _ = window.New

// Matcher is a hash-chain matcher. A single Matcher is not safe for
// concurrent use.
type Matcher struct{}

// New creates a Matcher. win supplies historical bytes (distance lookups),
// windowSize is its capacity and maxChain bounds the candidate-chain depth.
func New(win *window.Window, windowSize, maxChain int) *Matcher { return &Matcher{} }

// Register inserts the 3-byte sequence starting at tail[0] into the chains.
// It must be called once per confirmed input byte, in input order, with a
// tail of at least 3 bytes (shorter tails are ignored).
func (m *Matcher) Register(tail []byte) {}

// Find looks for the longest match for pending[0:] among historical bytes.
// pending supplies forward lookahead beyond the window. It returns the
// distance (0 if none) and matched length.
func (m *Matcher) Find(pending []byte) (dist, length int) { return 0, 0 }

// Candidates reports the total number of candidate positions examined.
func (m *Matcher) Candidates() uint64 { return 0 }
