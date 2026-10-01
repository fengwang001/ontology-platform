package ontology

import (
	"errors"
	"testing"
)

func addOrFail(t *testing.T, ix *Index, docID string, terms []string) {
	t.Helper()
	if err := ix.Add(docID, terms); err != nil {
		t.Fatalf("Add(%q, %v) unexpected error: %v", docID, terms, err)
	}
}

func mustPhrase(t *testing.T, ix *Index, q []string, slop, maxGap, k int) []Result {
	t.Helper()
	got, err := ix.Phrase(q, slop, maxGap, k)
	if err != nil {
		t.Fatalf("Phrase(%v, slop=%d, maxGap=%d, k=%d) unexpected error: %v", q, slop, maxGap, k, err)
	}
	return got
}

func eqResults(got, want []Result) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Exact adjacent phrase (slop 0), and total gap equal to slop vs slop+1.
func TestSlopBoundary(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "x", "b", "x", "x", "b"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); len(got) != 0 {
		t.Fatalf("slop 0: got %v, want no match", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 1, 100, 10); !eqResults(got, []Result{{"d1", 1, 1}}) {
		t.Fatalf("slop 1 (gap == slop): got %v", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 2, 100, 10); !eqResults(got, []Result{{"d1", 1, 1}}) {
		t.Fatalf("slop 2 (gap 3 == slop+1 stays out): got %v", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 3, 100, 10); !eqResults(got, []Result{{"d1", 1, 1}}) {
		t.Fatalf("slop 3: got %v", got)
	}
}

// Per-step gap equal to maxGap and maxGap+1; nearest middle term invalid.
func TestPerStepMaxGapExample(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "b", "x", "b", "x", "c"})
	if got := mustPhrase(t, ix, []string{"a", "b", "c"}, 3, 2, 10); !eqResults(got, []Result{{"d1", 1, 3}}) {
		t.Fatalf("maxGap 2: got %v, want freq=1 minGap=3", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b", "c"}, 3, 1, 10); len(got) != 0 {
		t.Fatalf("maxGap 1: got %v, want empty", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b", "c"}, 3, 3, 10); !eqResults(got, []Result{{"d1", 1, 3}}) {
		t.Fatalf("maxGap 3: got %v, want freq=1 minGap=3", got)
	}
}

// Both spec sentence-boundary examples.
func TestBoundaryExamples(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "b", Boundary, "a", "b", "a", "x", "b"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); !eqResults(got, []Result{{"d1", 2, 0}}) {
		t.Fatalf("[a,b] slop 0: got %v, want freq=2 minGap=0", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 1, 100, 10); !eqResults(got, []Result{{"d1", 3, 0}}) {
		t.Fatalf("[a,b] slop 1: got %v, want freq=3 minGap=0", got)
	}
	if got := mustPhrase(t, ix, []string{"b", "a"}, 2, 100, 10); !eqResults(got, []Result{{"d1", 1, 0}}) {
		t.Fatalf("[b,a] slop 2: got %v, want freq=1 minGap=0", got)
	}
}

// Boundary exactly at the edges of the match interval.
func TestBoundaryAdjacent(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"x", "x", Boundary, "a", "b"})
	addOrFail(t, ix, "d2", []string{"a", Boundary, "b"})
	addOrFail(t, ix, "d3", []string{"a", "b", Boundary})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); !eqResults(got, []Result{
		{"d1", 1, 0}, {"d3", 1, 0},
	}) {
		t.Fatalf("boundary adjacency: got %v (d1,d3 match; d2 crosses boundary)", got)
	}
}

// Repeated query terms: [a,a] on [a,a,a] slop 0 -> freq 2, no reuse.
func TestRepeatedQueryTerm(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "a", "a"})
	if got := mustPhrase(t, ix, []string{"a", "a"}, 0, 100, 10); !eqResults(got, []Result{{"d1", 2, 0}}) {
		t.Fatalf("[a,a] on aaa: got %v, want freq=2", got)
	}
	if got := mustPhrase(t, ix, []string{"a", "a", "a"}, 0, 100, 10); !eqResults(got, []Result{{"d1", 1, 0}}) {
		t.Fatalf("[a,a,a] on aaa: got %v, want freq=1", got)
	}
}

// Reversed order never matches.
func TestReversedOrder(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"b", "x", "a"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 100, 100, 10); len(got) != 0 {
		t.Fatalf("reversed: got %v, want empty", got)
	}
}

// One first position counts once despite many middles; overlapping starts count.
func TestOverlappingAndDuplicate(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "b", "b", "c"})
	if got := mustPhrase(t, ix, []string{"a", "b", "c"}, 100, 100, 10); !eqResults(got, []Result{{"d1", 1, 1}}) {
		t.Fatalf("one start many middles: got %v (gap min is a0,b2,c3 = 1)", got)
	}
	addOrFail(t, ix, "d2", []string{"a", "b", "a", "b"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); !eqResults(got, []Result{{"d2", 2, 0}, {"d1", 1, 0}}) {
		t.Fatalf("overlapping starts: got %v (d1 has adjacent a0,b1 too)", got)
	}
}

// Single term query: freq equals term frequency, minGap 0.
func TestSingleTerm(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", Boundary, "a", "x", "a"})
	if got := mustPhrase(t, ix, []string{"a"}, 0, 0, 10); !eqResults(got, []Result{{"d1", 3, 0}}) {
		t.Fatalf("single term: got %v, want freq=3 minGap=0", got)
	}
}

// Ranking: freq desc, then minGap asc, then docID byte order; top-k applied.
func TestRankingAndK(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "z", []string{"a", "b", "a", "b"})
	addOrFail(t, ix, "m", []string{"a", "x", "b"})
	addOrFail(t, ix, "a", []string{"a", "x", "b"})
	want := []Result{{"z", 2, 0}, {"a", 1, 1}, {"m", 1, 1}}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 100, 100, 10); !eqResults(got, want) {
		t.Fatalf("ranking: got %v, want %v", got, want)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 100, 100, 2); !eqResults(got, want[:2]) {
		t.Fatalf("top-k: got %v", got)
	}
}

// Delete then re-add with the same docID behaves as a new document.
func TestDeleteAndReadd(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "d1", []string{"a", "x", "b"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); len(got) != 0 {
		t.Fatalf("before delete: got %v", got)
	}
	if err := ix.Delete("d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := mustPhrase(t, ix, []string{"a", "b"}, 100, 100, 10); len(got) != 0 {
		t.Fatalf("after delete: got %v", got)
	}
	addOrFail(t, ix, "d1", []string{"a", "b"})
	if got := mustPhrase(t, ix, []string{"a", "b"}, 0, 100, 10); !eqResults(got, []Result{{"d1", 1, 0}}) {
		t.Fatalf("after re-add: got %v", got)
	}
	if err := ix.Add("d1", []string{"a"}); !errors.Is(err, ErrDuplicateDoc) {
		t.Fatalf("duplicate add: got %v", err)
	}
}

// Validation order and rejected operations must not mutate the index.
func TestValidationAndNoMutation(t *testing.T) {
	ix := NewIndex()
	if err := ix.Add("", []string{"a"}); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("empty docID: %v", err)
	}
	if err := ix.Add("d", nil); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("empty terms: %v", err)
	}
	if err := ix.Add("d", []string{"a", ""}); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("empty term: %v", err)
	}
	if err := ix.Delete("missing"); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := ix.Phrase(nil, 0, 0, 1); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("empty query: %v", err)
	}
	if _, err := ix.Phrase(make([]string, 9), 0, 0, 1); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("9-term query: %v", err)
	}
	if _, err := ix.Phrase([]string{"a", ""}, 0, 0, 1); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("empty query term: %v", err)
	}
	if _, err := ix.Phrase([]string{"a", Boundary}, 0, 0, 1); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("boundary in query: %v", err)
	}
	if _, err := ix.Phrase([]string{"a"}, -1, 0, 1); !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("slop -1: %v", err)
	}
	if _, err := ix.Phrase([]string{"a"}, 101, 0, 1); !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("slop 101: %v", err)
	}
	if _, err := ix.Phrase([]string{"a"}, 0, -1, 1); !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("maxGap -1: %v", err)
	}
	if _, err := ix.Phrase([]string{"a"}, 0, 101, 1); !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("maxGap 101: %v", err)
	}
	if _, err := ix.Phrase([]string{"a"}, 0, 0, 0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("k 0: %v", err)
	}
	// Index state must be unaffected by every rejected call above.
	if err := ix.Add("d", []string{"a"}); err != nil {
		t.Fatalf("valid add after rejections: %v", err)
	}
	if err := ix.Add("d", []string{"b"}); !errors.Is(err, ErrDuplicateDoc) {
		t.Fatalf("duplicate: %v", err)
	}
	if got := mustPhrase(t, ix, []string{"a"}, 0, 0, 10); !eqResults(got, []Result{{"d", 1, 0}}) {
		t.Fatalf("state after rejections: got %v", got)
	}
	if got := mustPhrase(t, ix, []string{"zzz"}, 0, 0, 10); len(got) != 0 {
		t.Fatalf("missing terms is empty result, got %v", got)
	}
}

// Determinism: identical repeated queries return field-identical output.
func TestDeterministicRepeatedQuery(t *testing.T) {
	ix := NewIndex()
	addOrFail(t, ix, "b", []string{"a", "x", "b"})
	addOrFail(t, ix, "a", []string{"a", "b", "x", "a", "b"})
	first := mustPhrase(t, ix, []string{"a", "b"}, 1, 100, 10)
	for i := 0; i < 5; i++ {
		if got := mustPhrase(t, ix, []string{"a", "b"}, 1, 100, 10); !eqResults(got, first) {
			t.Fatalf("query %d not deterministic: %v vs %v", i, got, first)
		}
	}
}
