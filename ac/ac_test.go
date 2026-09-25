package ac

import (
	"strings"
	"testing"

	"ontology/trie"
)

// TestComparisonsLinear pins the O(n) bound: at most 2n char/edge
// comparisons even for worst-shaped inputs at several scales.
func TestComparisonsLinear(t *testing.T) {
	patSets := [][]string{
		{"a"},
		{strings.Repeat("a", 50)},
		{strings.Repeat("a", 50), "b"},
		{"ab", "ba", "aba", "bab"},
	}
	texts := []func(int) string{
		func(n int) string { return strings.Repeat("a", n) },
		func(n int) string { return strings.Repeat("ab", n/2+1)[:n] },
	}
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		for pi, pats := range patSets {
			tr := trie.New()
			for i, p := range pats {
				tr.Insert(p, i)
			}
			tr.Build()
			for ti, mk := range texts {
				a := NewAutomaton(tr)
				a.Match(mk(n))
				if c := a.cmps; c > int64(2*n) {
					t.Errorf("n=%d patSet=%d text=%d: %d comparisons > 2n", n, pi, ti, c)
				}
			}
		}
	}
}
