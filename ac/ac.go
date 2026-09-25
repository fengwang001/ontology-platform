// Package ac scans text with the automaton built by package trie and
// reports every occurrence of every pattern, overlaps included.
package ac

import (
	"sync/atomic"

	"ontology/trie"
)

// Match is one occurrence: pattern index and the offset just past the
// matched substring, so it occupies text[End-len(pattern):End].
type Match struct {
	Pattern int
	End     int
}

// Automaton is the built matcher. After construction it is read-only
// except for an atomic counter, so Match is safe for concurrent use.
type Automaton struct {
	root *trie.Node
	cmps int64 // char/edge comparisons; unexported on purpose, atomic
}

// NewAutomaton wraps a built trie as a scanner.
func NewAutomaton(t *trie.Trie) *Automaton { return &Automaton{root: t.Root()} }

// Match scans text once and reports every occurrence of every pattern.
// At each landed node it reports the node's own terminal patterns first,
// then those along the fail chain (the trie flattens this into Output).
func (a *Automaton) Match(text string) []Match {
	var out []Match
	n := a.root
	for i := 0; i < len(text); i++ {
		c := text[i]
		for {
			atomic.AddInt64(&a.cmps, 1)
			if nx, ok := n.Child(c); ok {
				n = nx
				break
			}
			if n == a.root {
				break
			}
			n = n.Fail()
		}
		for _, p := range n.Output() {
			out = append(out, Match{Pattern: p, End: i + 1})
		}
	}
	return out
}
