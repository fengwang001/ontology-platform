package lr

import (
	"fmt"
	"testing"

	"ontology/gram"
)

// bigGrammar: nonterminal M has m productions M -> t_i, seeded from [S -> ·M,$].
// Closure adds exactly m items [M -> ·t_i,$], all distinct.
func bigGrammar(m int) (*gram.Grammar, []Item) {
	terms := make([]gram.Symbol, m)
	prods := make([]gram.Production, m)
	for i := 0; i < m; i++ {
		terms[i] = gram.Symbol(fmt.Sprintf("t%d", i))
		prods[i] = gram.Production{Head: "M", Body: []gram.Symbol{terms[i]}}
	}
	prods = append(prods, gram.Production{Head: "S", Body: []gram.Symbol{"M"}})
	g, err := gram.New("S", terms, []gram.Symbol{"S", "M"}, prods)
	if err != nil {
		panic(err)
	}
	return g, []Item{{Head: "S", Body: []gram.Symbol{"M"}, Lookahead: "$"}}
}

// TestWorkQueueNoProcessTwice pins invariant "each item processed at most
// once": across many sizes m, the non-exported rechecked counter (items
// generated a second+ time) must stay 0 — independent of m. The counter is
// read only here, inside package lr, never through an exported API.
func TestWorkQueueNoProcessTwice(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		g, seed := bigGrammar(m)
		out, st, err := closureRun(g, seed)
		if err != nil {
			t.Fatalf("m=%d unexpected error: %v", m, err)
		}
		if len(out) != m+1 {
			t.Fatalf("m=%d: want %d closure items, got %d", m, m+1, len(out))
		}
		if st.rechecked != 0 {
			t.Fatalf("m=%d: %d items reprocessed; want 0 (work queue, not full rescans)", m, st.rechecked)
		}
	}
}

// TestCounterSeesDuplicatesButNoReprocess: with a grammar that regenerates the
// same candidate, rechecked goes positive yet every distinct item is still
// processed exactly once (result is fully deduplicated and closed).
func TestCounterSeesDuplicatesButNoReprocess(t *testing.T) {
	// Two identical S productions make [S -> ·A,$] get generated twice; then A
	// is expanded. Duplicates must bump the counter without duplicate closure.
	g, err := gram.New("P", []gram.Symbol{"a"}, []gram.Symbol{"P", "S", "A"}, []gram.Production{
		{Head: "P", Body: []gram.Symbol{"S"}},
		{Head: "S", Body: []gram.Symbol{"A"}},
		{Head: "S", Body: []gram.Symbol{"A"}}, // duplicate production
		{Head: "A", Body: []gram.Symbol{"a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	seed := []Item{{Head: "P", Body: []gram.Symbol{"S"}, Lookahead: "$"}}
	out, st, err := closureRun(g, seed)
	if err != nil {
		t.Fatal(err)
	}
	if st.rechecked == 0 {
		t.Fatalf("expected rechecked > 0 under duplicate generation, got 0")
	}
	// Distinct items only: [P->·S,$] [S->·A,$] [A->·a,$] => 3.
	if len(out) != 3 {
		t.Fatalf("want 3 deduplicated items, got %d: %v", len(out), out)
	}
}

// TestClassicClosureSixItems is the NOTES.md derivation result.
func TestClassicClosureSixItems(t *testing.T) {
	g, err := gram.New("S'", []gram.Symbol{"c", "d"}, []gram.Symbol{"S'", "S", "C"}, []gram.Production{
		{Head: "S'", Body: []gram.Symbol{"S"}},
		{Head: "S", Body: []gram.Symbol{"C", "C"}},
		{Head: "C", Body: []gram.Symbol{"c", "C"}},
		{Head: "C", Body: []gram.Symbol{"d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Closure(g, []Item{{Head: "S'", Body: []gram.Symbol{"S"}, Lookahead: "$"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 6 {
		t.Fatalf("want 6 items, got %d: %v", len(out), out)
	}
}
