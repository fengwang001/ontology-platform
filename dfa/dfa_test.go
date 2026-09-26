package dfa

import (
	"fmt"
	"sync"
	"testing"

	"ontology/nfa"
)

// section3 是 NOTES.md 第三节的 NFA：0-a->1, 1-ε->2, 1-b->1, 2-c->2。
func section3() *nfa.NFA {
	return &nfa.NFA{
		N: 3,
		Trans: []map[byte][]int{
			{'a': {1}},
			{nfa.Epsilon: {2}, 'b': {1}},
			{'c': {2}},
		},
		Alphabet: []byte{'a', 'b', 'c'},
		Start:    0,
		Accept:   []int{2},
	}
}

func TestAcceptsTable(t *testing.T) {
	d := Build(section3())
	cases := []struct {
		in   string
		want bool
	}{
		{"a", true}, {"ab", true}, {"ac", true}, {"abb", true},
		{"abc", true}, {"acc", true}, {"abbc", true},
		{"", false}, {"b", false}, {"z", false}, {"ba", false}, {"acz", false},
	}
	for _, c := range cases {
		if got := d.Accepts(c.in); got != c.want {
			t.Errorf("Accepts(%q)=%v, want %v", c.in, got, c.want)
		}
	}
}

func TestClosureComplete(t *testing.T) {
	n := section3()
	d := Build(n)
	for _, st := range d.States {
		if got := n.EpsilonClosure(st); fmt.Sprint(got) != fmt.Sprint(st) {
			t.Errorf("state %v is not a full epsilon closure: closure=%v", st, got)
		}
	}
}

func TestDeterministic(t *testing.T) {
	d := Build(section3())
	if len(d.index) != len(d.States) {
		t.Fatalf("duplicate state sets: %d states but %d keys", len(d.States), len(d.index))
	}
	for cur, row := range d.Trans {
		seen := map[byte]bool{}
		for c := range row { // map[byte]int 结构上每字符至多一个后继
			if seen[c] {
				t.Fatalf("state %d has two successors on %q", cur, c)
			}
			seen[c] = true
		}
	}
}

func TestDedupCounterConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		n := chainNFA(m)
		d := Build(n)
		if len(d.States) != m {
			t.Fatalf("m=%d: got %d DFA states", m, len(d.States))
		}
		d.dedupChecks = 0
		if _, isNew := d.intern(n.EpsilonClosure(n.Trans[0]['a'])); isNew {
			t.Fatalf("m=%d: {1} should already exist", m)
		}
		d.intern([]int{0, m - 1}) // 全新集合
		if d.dedupChecks > 2 {
			t.Fatalf("m=%d: dedup checks=%d, want <=2 (hash lookup, not linear scan)", m, d.dedupChecks)
		}
	}
}

func TestConcurrentAccepts(t *testing.T) {
	d := Build(section3())
	inputs := []string{"a", "ab", "ac", "abb", "b", "", "abc", "z", "acc", "abbc"}
	want := make([]bool, len(inputs))
	for i, s := range inputs {
		want[i] = d.Accepts(s)
	}
	const G = 32
	results := make([][]bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]bool, len(inputs))
			for i, s := range inputs {
				r[i] = d.Accepts(s)
			}
			results[g] = r
		}(g)
	}
	wg.Wait()
	for g := range results {
		for i := range want {
			if results[g][i] != want[i] {
				t.Fatalf("goroutine %d input %q: got %v, want %v", g, inputs[i], results[g][i], want[i])
			}
		}
	}
}
