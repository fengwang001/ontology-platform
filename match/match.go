// Package match finds longest matches over a sliding window.
package match

import "ontology/window"

// MinMatch is the shortest match emitted.
const MinMatch = 3

// Matcher is a hash-chain matcher.
type Matcher struct {
	win     *window.Window
	chain   int
	examined int64
}

// New constructs a matcher with a window and bounded chain length.
func New(win *window.Window, maxChain int) *Matcher { return &Matcher{} }

// Seed inserts bytes from a preset dictionary without emitting.
func (m *Matcher) Seed(p []byte) {}

// Examined returns the total candidate positions inspected.
func (m *Matcher) Examined() int64 { return 0 }

// Find returns the best distance/length for p[0:] capped by maxLen.
// found is false when length < MinMatch.
func (m *Matcher) Find(p []byte, maxLen int) (dist, length int, found bool) { return 0, 0, false }

// Advance inserts the next n bytes of p into history.
func (m *Matcher) Advance(p []byte, n int) {}
