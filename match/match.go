// Package match implements a hash-chain longest-match finder.
package match

import "ontology/window"

// Matcher maintains hash chains over the bytes of a sliding window.
type Matcher struct {
	win *window.Window
}

// New builds a Matcher with bounded candidate chains.
func New(win *window.Window, maxChain int) *Matcher {
	return &Matcher{win: win}
}

// Find searches the window for the longest match starting one byte before the
// newest window byte; lookahead supplies bytes not yet in the window.
func (m *Matcher) Find(lookahead []byte) (distance, length int) { return 0, 0 }

// Append pushes one byte to the window and records it in the hash chains.
func (m *Matcher) Append(b byte) {}

// Probes reports the total number of candidate positions examined.
func (m *Matcher) Probes() uint64 { return 0 }
