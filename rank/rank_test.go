package rank

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"ontology/trie"
)

func naiveTopK(freq map[string]int, prefix string, k int) []string {
	var c []string
	for s := range freq {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			c = append(c, s)
		}
	}
	sort.Slice(c, func(i, j int) bool {
		if freq[c[i]] != freq[c[j]] {
			return freq[c[i]] > freq[c[j]]
		}
		return c[i] < c[j]
	})
	return c[:min(len(c), k)]
}

// TopK must equal the naive collect-sort-truncate reference.
func TestTopKMatchesNaive(t *testing.T) {
	cases := []struct {
		name   string
		freq   map[string]int
		prefix string
		k      int
	}{
		{"tie broken lexicographically", map[string]int{"apricot": 5, "apple": 5, "app": 3, "application": 2}, "ap", 3},
		{"k exceeds candidates", map[string]int{"ab": 1, "ac": 2}, "a", 10},
		{"prefix excludes others", map[string]int{"ba": 9, "bb": 8, "ca": 7}, "b", 2},
		{"unicode strings", map[string]int{"苹果": 3, "苹菓": 3, "梨子": 9}, "苹", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := trie.New()
			for s, f := range c.freq {
				tr.Insert(s, f)
			}
			got := TopK(tr, c.prefix, c.k)
			if want := naiveTopK(c.freq, c.prefix, c.k); !reflect.DeepEqual(got, want) {
				t.Errorf("TopK=%v, naive=%v", got, want)
			}
			for i := 1; i < len(got); i++ { // invariant 2: sorted by (freq desc, lex asc)
				fi, fj := c.freq[got[i-1]], c.freq[got[i]]
				if fi < fj || (fi == fj && got[i-1] > got[i]) {
					t.Errorf("order violated at %d: %v", i, got)
				}
			}
		})
	}
}

// TopK must collect exactly d candidates (d = matches), independent of m.
func TestCandidateCountIndependentOfSize(t *testing.T) {
	const d = 7
	for _, m := range []int{100, 1000, 10000} {
		tr := trie.New()
		for i := 0; i < m; i++ {
			tr.Insert(fmt.Sprintf("x%05d", i), i+1)
		}
		for i := 0; i < d; i++ {
			tr.Insert(fmt.Sprintf("pre%03d", i), i+1)
		}
		collected.Store(0)
		if got := TopK(tr, "pre", 3); len(got) != 3 {
			t.Errorf("m=%d: TopK returned %d, want 3", m, len(got))
		}
		if c := collected.Load(); c != d {
			t.Errorf("m=%d: collected %d candidates, want %d", m, c, d)
		}
	}
}
