package phrase

import (
	"reflect"
	"testing"

	"ontology/posting"
)

// listsFromDocs builds one posting list per term from token sequences.
func listsFromDocs(docs ...[]string) map[string]posting.List {
	builders := map[string]*posting.Builder{}
	for d, tokens := range docs {
		for p, tok := range tokens {
			b := builders[tok]
			if b == nil {
				b = &posting.Builder{}
				builders[tok] = b
			}
			b.Add(uint32(d), uint32(p))
		}
	}
	out := map[string]posting.List{}
	for term, b := range builders {
		out[term] = b.List()
	}
	return out
}

func phraseOf(index map[string]posting.List, terms ...string) []Hit {
	lists := make([]posting.List, len(terms))
	for i, t := range terms {
		lists[i] = index[t]
	}
	return Phrase(lists)
}

func TestPhraseHits(t *testing.T) {
	cases := []struct {
		name  string
		docs  [][]string
		terms []string
		want  []Hit
	}{
		{"overlap AAA", [][]string{{"A", "A", "A"}}, []string{"A", "A"},
			[]Hit{{Doc: 0, Starts: []uint32{0, 1}}}},
		{"overlap AAAA", [][]string{{"A", "A", "A", "A"}}, []string{"A", "A"},
			[]Hit{{Doc: 0, Starts: []uint32{0, 1, 2}}}},
		{"overlap ABABA", [][]string{{"A", "B", "A", "B", "A"}}, []string{"A", "B", "A"},
			[]Hit{{Doc: 0, Starts: []uint32{0, 2}}}},
		{"single term degenerates", [][]string{{"A", "B", "A"}}, []string{"A"},
			[]Hit{{Doc: 0, Starts: []uint32{0, 2}}}},
		{"phrase longer than doc", [][]string{{"A", "B"}}, []string{"A", "B", "C"}, nil},
		{"missing term", [][]string{{"A", "B"}}, []string{"A", "ZZZ"}, nil},
		{"empty token legal", [][]string{{"", "A"}}, []string{"", "A"},
			[]Hit{{Doc: 0, Starts: []uint32{0}}}},
		{"multi doc", [][]string{{"X", "A", "B"}, {"A", "B"}, {"B", "A"}}, []string{"A", "B"},
			[]Hit{{Doc: 0, Starts: []uint32{1}}, {Doc: 1, Starts: []uint32{0}}}},
		{"empty index", nil, []string{"A", "B"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := phraseOf(listsFromDocs(tc.docs...), tc.terms...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestPhraseComparisonBound(t *testing.T) {
	const n = 10000
	tokens := make([]string, 0, 2*n)
	for i := 0; i < n; i++ {
		tokens = append(tokens, "A", "B")
	}
	index := listsFromDocs(tokens)
	ResetCounters()
	hits := phraseOf(index, "A", "B")
	if len(hits) != 1 || len(hits[0].Starts) != n {
		t.Fatalf("want %d hits in one doc, got %v", n, hits)
	}
	if got, bound := PosComparisons(), int64(4*(n+n)); got > bound {
		t.Fatalf("comparisons %d exceed bound %d", got, bound)
	}
}

func TestAnd(t *testing.T) {
	docs := func(lo, hi uint32) posting.List {
		var l posting.List
		for d := lo; d < hi; d++ {
			l = append(l, posting.Entry{Doc: d, Pos: []uint32{0}})
		}
		return l
	}
	cases := []struct {
		name  string
		lists []posting.List
		want  []uint32
	}{
		{"basic", []posting.List{docs(0, 100), docs(50, 200), docs(0, 80)}, docs(50, 80).Docs()},
		{"one empty", []posting.List{docs(0, 10), nil}, nil},
		{"no lists", nil, nil},
		{"single list", []posting.List{docs(3, 6)}, []uint32{3, 4, 5}},
		{"disjoint", []posting.List{docs(0, 5), docs(5, 9)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := And(tc.lists); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestAndShortestDriven(t *testing.T) {
	docs := func(lo, hi uint32) posting.List {
		var l posting.List
		for d := lo; d < hi; d++ {
			l = append(l, posting.Entry{Doc: d, Pos: []uint32{0}})
		}
		return l
	}
	const shortest = 100
	lists := []posting.List{docs(0, 150), docs(0, shortest), docs(0, 120)}
	ResetCounters()
	got := And(lists)
	if len(got) != shortest {
		t.Fatalf("want %d docs, got %d", shortest, len(got))
	}
	if c, bound := AndComparisons(), int64(4*shortest*len(lists)); c > bound {
		t.Fatalf("comparisons %d exceed bound %d", c, bound)
	}
}
