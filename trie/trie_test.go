package trie

import (
	"math/rand"
	"testing"
)

func genStrings(m, l int, r *rand.Rand) []string {
	seen := map[string]bool{}
	var out []string
	for len(out) < m {
		b := make([]byte, l)
		for i := range b {
			b[i] = byte('a' + r.Intn(26))
		}
		if s := string(b); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Single Insert/Delete must touch O(len) nodes, independent of library size.
func TestNodeVisitsIndependentOfSize(t *testing.T) {
	const L = 16
	for _, m := range []int{100, 1000, 10000} {
		r := rand.New(rand.NewSource(int64(m)))
		tr := New()
		for _, s := range genStrings(m, L, r) {
			tr.Insert(s, 1)
		}
		extra := genStrings(1, L, r)[0]
		tr.visited = 0
		tr.Insert(extra, 1)
		if got := tr.visited; got > L+2 {
			t.Errorf("m=%d: insert visited %d nodes, want <= %d", m, got, L+2)
		}
		tr.visited = 0
		if !tr.Delete(extra) {
			t.Fatalf("m=%d: delete of inserted string failed", m)
		}
		if got := tr.visited; got > 2*L+4 {
			t.Errorf("m=%d: delete visited %d nodes, want <= %d", m, got, 2*L+4)
		}
	}
}

// Reference counts must match naive recounts; pruning must remove exactly
// the branches with no remaining strings.
func TestRefsAndPrune(t *testing.T) {
	cases := []struct {
		name  string
		ins   []string
		del   string
		under map[string]int // prefix -> terminal count expected after delete
	}{
		{"prefix terminal kept", []string{"app", "apple", "application"}, "app",
			map[string]int{"app": 2, "appl": 2}},
		{"leaf prunes chain", []string{"abc", "abd"}, "abc",
			map[string]int{"ab": 1, "abc": 0}},
		{"last string prunes all", []string{"xyz"}, "xyz",
			map[string]int{"x": 0, "": 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := New()
			for i, s := range c.ins {
				tr.Insert(s, i+1)
			}
			if !tr.Delete(c.del) {
				t.Fatal("delete failed")
			}
			if !tr.CheckRefs() {
				t.Error("refs inconsistent after delete")
			}
			if tr.Count() != len(c.ins)-1 {
				t.Errorf("Count()=%d, want %d", tr.Count(), len(c.ins)-1)
			}
			for p, want := range c.under {
				got := 0
				tr.Walk(p, func(string, int) { got++ })
				if got != want {
					t.Errorf("Walk(%q) found %d, want %d", p, got, want)
				}
			}
		})
	}
	// Accumulating insert must not change reference counts.
	tr := New()
	tr.Insert("app", 3)
	tr.Insert("app", 4)
	if tr.Count() != 1 || !tr.CheckRefs() {
		t.Error("duplicate insert broke refs")
	}
	got := 0
	tr.Walk("app", func(_ string, f int) { got = f })
	if got != 7 {
		t.Errorf("freq=%d, want 7", got)
	}
}
