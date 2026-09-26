package nfa

import (
	"strings"
	"testing"

	"ontology/reast"
)

// matchSteps replays Match and returns the epsilon-closed state set after
// each character step (including the initial closure).
func matchSteps(n *NFA, s string) []map[int]struct{} {
	var out []map[int]struct{}
	cur := n.closure(map[int]struct{}{n.Start: {}})
	out = append(out, cloneSet(cur))
	for i := 0; i < len(s); i++ {
		nxt := map[int]struct{}{}
		for st := range cur {
			for _, to := range n.charFrom[st][s[i]] {
				nxt[to] = struct{}{}
			}
		}
		cur = n.closure(nxt)
		out = append(out, cloneSet(cur))
	}
	return out
}

func cloneSet(in map[int]struct{}) map[int]struct{} {
	out := make(map[int]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

// TestEpsilonClosureInvariant pins invariant (2): after every character
// step the current state set must already be its own epsilon closure, so
// closing it again adds nothing.
func TestEpsilonClosureInvariant(t *testing.T) {
	cases := []struct {
		pattern string
		inputs  []string
	}{
		{"a(b|c)*", []string{"", "a", "ab", "ac", "abb", "b", "abcbc", "accca"}},
		{"(ab)+", []string{"", "ab", "abab", "aba", "ababab"}},
		{"a?b+c*", []string{"", "b", "ab", "bc", "aaa", "abcc"}},
		{"((a|b)*c)+", []string{"", "c", "ac", "abc", "abab", "cc"}},
	}
	for _, c := range cases {
		re, err := reast.Parse(c.pattern)
		if err != nil {
			t.Fatalf("parse %q: %v", c.pattern, err)
		}
		n := Compile(re)
		for _, s := range c.inputs {
			for i, set := range matchSteps(n, s) {
				again := n.closure(cloneSet(set))
				if len(again) != len(set) {
					t.Fatalf("%q step %d on %q: closure not fixed (%d vs %d)",
						c.pattern, i, s, len(again), len(set))
				}
			}
		}
	}
}

// TestAppendTouchedConstant pins section (4): appending one char to an
// m-state NFA reads/modifies only a constant number of existing states,
// independent of m. touched is unexported and read only inside this pkg.
func TestAppendTouchedConstant(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		pat := strings.Repeat("a", m)
		re, err := reast.Parse(pat)
		if err != nil {
			t.Fatalf("m=%d parse: %v", m, err)
		}
		n := Compile(re)
		if n.N != 2*m {
			t.Fatalf("m=%d: want %d states, got %d", m, 2*m, n.N)
		}
		oldEps := make([][]int, n.N)
		oldChar := len(n.Char)
		for i := range oldEps {
			oldEps[i] = append(oldEps[i], n.Eps[i]...)
		}
		oldAccept := n.Accept
		n.AppendChar('b')
		if n.touched > 2 {
			t.Fatalf("m=%d: append touched %d existing states, want <= 2", m, n.touched)
		}
		if n.N != 2*m+2 || len(n.Char) != oldChar+1 {
			t.Fatalf("m=%d: unexpected growth after append", m)
		}
		for i := 0; i < 2*m; i++ {
			want := len(oldEps[i])
			if i == oldAccept {
				want++ // exactly one epsilon edge leaves the old accept
			}
			if len(n.Eps[i]) != want {
				t.Fatalf("m=%d: existing state %d changed unexpectedly", m, i)
			}
		}
		if !n.Match(pat+"b") || n.Match(pat) {
			t.Fatalf("m=%d: language wrong after append", m)
		}
	}
}
