package phrase

import (
	"reflect"
	"testing"

	"ontology/posting"
)

// docLists 由一篇文档的词元序列构造各词倒排链。
func docLists(tokens []string) map[string]posting.List {
	bs := map[string]*posting.Builder{}
	for i, tok := range tokens {
		b, ok := bs[tok]
		if !ok {
			b = &posting.Builder{}
			bs[tok] = b
		}
		if err := b.Add(0, uint32(i)); err != nil {
			panic(err)
		}
	}
	out := map[string]posting.List{}
	for t, b := range bs {
		out[t] = b.List()
	}
	return out
}

func phraseOf(tokens []string, phrase ...string) ([]Hit, int) {
	lists := docLists(tokens)
	var qs []posting.List
	for _, w := range phrase {
		qs = append(qs, lists[w]) // 不存在的词 -> nil 链
	}
	var st Stats
	return Phrase(qs, &st), st.Comparisons()
}

func TestPhraseHits(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		phrase []string
		want   []Hit
	}{
		{"overlap AAA", []string{"A", "A", "A"}, []string{"A", "A"},
			[]Hit{{0, 0}, {0, 1}}},
		{"overlap AAAA", []string{"A", "A", "A", "A"}, []string{"A", "A"},
			[]Hit{{0, 0}, {0, 1}, {0, 2}}},
		{"overlap ABABA", []string{"A", "B", "A", "B", "A"}, []string{"A", "B", "A"},
			[]Hit{{0, 0}, {0, 2}}},
		{"single word phrase", []string{"x", "y", "x"}, []string{"x"},
			[]Hit{{0, 0}, {0, 2}}},
		{"phrase longer than doc", []string{"A", "B"}, []string{"A", "B", "C"}, nil},
		{"term absent", []string{"A", "B"}, []string{"A", "ZZZ"}, nil},
		{"empty token legal", []string{"", "A", ""}, []string{"", "A"},
			[]Hit{{0, 0}}},
		{"single doc single term", []string{"only"}, []string{"only"},
			[]Hit{{0, 0}}},
		{"no match in doc", []string{"A", "C", "B"}, []string{"A", "B"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := phraseOf(tc.tokens, tc.phrase...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPhraseEmptyIndex(t *testing.T) {
	var st Stats
	if got := Phrase(nil, &st); got != nil {
		t.Fatalf("nil lists: got %v", got)
	}
	empty := posting.List{}
	if got := Phrase([]posting.List{empty, empty}, &st); got != nil {
		t.Fatalf("empty lists: got %v", got)
	}
}

func TestPhraseComparisonBound(t *testing.T) {
	const n = 10000
	tokens := make([]string, 0, 2*n)
	for i := 0; i < n; i++ {
		tokens = append(tokens, "A", "B")
	}
	hits, cmp := phraseOf(tokens, "A", "B")
	if len(hits) != n {
		t.Fatalf("hits: want %d, got %d", n, len(hits))
	}
	if bound := 4 * (n + n); cmp > bound {
		t.Fatalf("comparisons %d exceed bound %d", cmp, bound)
	}
}

func TestAnd(t *testing.T) {
	mk := func(docs ...uint32) posting.List {
		var b posting.Builder
		for _, d := range docs {
			if err := b.Add(d, 0); err != nil {
				panic(err)
			}
		}
		return b.List()
	}
	cases := []struct {
		name  string
		lists []posting.List
		want  []uint32
	}{
		{"basic", []posting.List{mk(1, 2, 3), mk(2, 3, 4)}, []uint32{2, 3}},
		{"disjoint", []posting.List{mk(1, 3), mk(2, 4)}, nil},
		{"empty list", []posting.List{mk(1), {}}, nil},
		{"single list", []posting.List{mk(5, 6)}, []uint32{5, 6}},
		{"three lists", []posting.List{mk(1, 2, 3, 4), mk(2, 4), mk(0, 2, 4, 6)},
			[]uint32{2, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var st Stats
			got := And(tc.lists, &st)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAndComparisonBound(t *testing.T) {
	mk := func(step, count uint32) posting.List {
		var b posting.Builder
		for i := uint32(0); i < count; i++ {
			if err := b.Add(i*step, 0); err != nil {
				panic(err)
			}
		}
		return b.List()
	}
	lists := []posting.List{mk(6, 100), mk(2, 150), mk(3, 200)} // 最短链长 100
	var st Stats
	got := And(lists, &st)
	if len(got) == 0 {
		t.Fatal("want non-empty intersection")
	}
	if bound := 4 * 100 * 3; st.Comparisons() > bound {
		t.Fatalf("com comparisons %d exceed bound %d", st.Comparisons(), bound)
	}
}
