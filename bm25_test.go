package ontology

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, idx *Index, docID string, terms ...string) {
	t.Helper()
	if _, err := idx.Add(docID, terms); err != nil {
		t.Fatalf("Add(%q, %v): %v", docID, terms, err)
	}
}

func mustUpdate(t *testing.T, idx *Index, docID string, terms ...string) {
	t.Helper()
	if _, err := idx.Update(docID, terms); err != nil {
		t.Fatalf("Update(%q, %v): %v", docID, terms, err)
	}
}

func mustDelete(t *testing.T, idx *Index, docID string) {
	t.Helper()
	if _, err := idx.Delete(docID); err != nil {
		t.Fatalf("Delete(%q): %v", docID, err)
	}
}

func assertErrorIs(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertResults(t *testing.T, got []Result, want ...Result) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result %d = %+v, want %+v; all=%v", i, got[i], want[i], got)
		}
	}
}

func TestSpecExample(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "a", "x", "y")
	mustAdd(t, idx, "b", "x")
	mustUpdate(t, idx, "a", "y")
	mustDelete(t, idx, "b")

	got, err := idx.Search([]string{"x"}, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, got,
		Result{DocID: "b", Score: "4/17"},
		Result{DocID: "a", Score: "4/23"},
	)

	got, err = idx.Search([]string{"x"}, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, got, Result{DocID: "b", Score: "1/1"})

	got, err = idx.Search([]string{"x"}, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, got)
}

func TestScoreEdges(t *testing.T) {
	t.Run("df equals N idf stays positive", func(t *testing.T) {
		idx := NewIndex()
		mustAdd(t, idx, "a", "x")
		mustAdd(t, idx, "b", "x")
		got, err := idx.Search([]string{"x"}, 10, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Score != "1/5" {
			t.Fatalf("score = %s, want 1/5", got[0].Score)
		}
	})

	t.Run("dl equals avgdl length term is one", func(t *testing.T) {
		idx := NewIndex()
		mustAdd(t, idx, "a", "x")
		mustAdd(t, idx, "b", "y")
		got, err := idx.Search([]string{"x"}, 10, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Score != "1/1" {
			t.Fatalf("score = %s, want 1/1", got[0].Score)
		}
	})

	t.Run("large tf", func(t *testing.T) {
		idx := NewIndex()
		terms := make([]string, 1000)
		for i := range terms {
			terms[i] = "x"
		}
		mustAdd(t, idx, "a", terms...)
		got, err := idx.Search([]string{"x"}, 10, 1)
		if err != nil {
			t.Fatal(err)
		}
		value, _ := new(big.Rat).SetString(got[0].Score)
		want := big.NewRat(5000, 6009)
		if value.Cmp(want) != 0 {
			t.Fatalf("score = %s, want %s", got[0].Score, want.RatString())
		}
	})
}

func TestLongDocumentChangesOrdering(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "a", "x")
	mustAdd(t, idx, "b", "y")
	before, err := idx.Search([]string{"x"}, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].DocID != "a" {
		t.Fatalf("before = %v", before)
	}

	long := make([]string, 100)
	for i := range long {
		long[i] = "z"
	}
	mustAdd(t, idx, "c", long...)
	after, err := idx.Search([]string{"x"}, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if after[0].DocID != "a" || after[0].Score == before[0].Score {
		t.Fatalf("after long document score did not change: %v", after)
	}
}

func TestTieBreaksDuplicateQueryAndK(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "c", "q")
	mustAdd(t, idx, "a", "q")
	mustAdd(t, idx, "b", "q")

	got, err := idx.Search([]string{"q", "q", "q"}, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, got,
		Result{DocID: "a", Score: "1/7"},
		Result{DocID: "b", Score: "1/7"},
	)
}

func TestVersionAndCompactionRules(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "a", "x")

	if _, err := idx.Search([]string{"x"}, 10, 2); !errors.Is(err, ErrVersionRange) {
		t.Fatalf("V+1: %v", err)
	}
	if _, err := idx.Search([]string{"x"}, 10, -1); !errors.Is(err, ErrVersionRange) {
		t.Fatalf("negative asOf: %v", err)
	}

	if _, err := idx.Compact(2); !errors.Is(err, ErrVersionRange) {
		t.Fatalf("keep V+1: %v", err)
	}
	if w, err := idx.Compact(1); err != nil || w != 1 {
		t.Fatalf("Compact(1) = %d, %v", w, err)
	}
	if w, err := idx.Compact(1); err != nil || w != 1 {
		t.Fatalf("Compact(current W) = %d, %v", w, err)
	}
	if _, err := idx.Compact(0); !errors.Is(err, ErrWatermarkRollback) {
		t.Fatalf("Compact(W-1): %v", err)
	}
	if _, err := idx.Search([]string{"x"}, 10, 0); !errors.Is(err, ErrVersionReclaimed) {
		t.Fatalf("W-1 query: %v", err)
	}
	got, err := idx.Search([]string{"x"}, 10, 1)
	if err != nil {
		t.Fatalf("asOf W: %v", err)
	}
	if len(got) != 1 || got[0] != (Result{DocID: "a", Score: "1/3"}) {
		t.Fatalf("at watermark = %v", got)
	}
}

func TestHighVersionResultsUnchangedAcrossCompact(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "a", "x", "y")
	mustAdd(t, idx, "b", "x")
	mustUpdate(t, idx, "a", "y", "z")
	before, err := idx.Search([]string{"x", "y"}, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Compact(2); err != nil {
		t.Fatal(err)
	}
	after, err := idx.Search([]string{"x", "y"}, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, after, before...)
}

func TestDeleteToZeroAndOne(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "a", "x")
	mustDelete(t, idx, "a")

	got, err := idx.Search([]string{"x"}, 10, 2)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty state got=%v err=%v", got, err)
	}
	mustAdd(t, idx, "a", "x")
	got, err = idx.Search([]string{"x"}, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertResults(t, got, Result{DocID: "a", Score: "1/3"})
}

func TestRejectionsDoNotConsumeVersion(t *testing.T) {
	idx := NewIndex()
	if _, err := idx.Add("", []string{"x"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := idx.Add("a", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := idx.Add("a", []string{""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	mustAdd(t, idx, "a", "x")
	if _, err := idx.Add("a", []string{"y"}); !errors.Is(err, ErrDuplicateDoc) {
		t.Fatal(err)
	}
	if _, err := idx.Update("missing", []string{"x"}); !errors.Is(err, ErrDocNotFound) {
		t.Fatal(err)
	}
	if _, err := idx.Delete("missing"); !errors.Is(err, ErrDocNotFound) {
		t.Fatal(err)
	}
	if _, err := idx.Search(nil, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := idx.Search([]string{"x"}, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if idx.Version() != 1 {
		t.Fatalf("version = %d, want 1", idx.Version())
	}
}

func TestConcurrentUpdateAtomicity(t *testing.T) {
	idx := NewIndex()
	mustAdd(t, idx, "doc", "old", "old")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = idx.Update("doc", []string{"new", "new", "new"})
		}()
		go func() {
			defer wg.Done()
			got, err := idx.Search([]string{"old", "new"}, 10, idx.Version())
			if err == nil && len(got) == 1 {
				score, _ := new(big.Rat).SetString(got[0].Score)
				if score == nil {
					t.Errorf("invalid score %q", got[0].Score)
				}
			}
		}()
	}
	wg.Wait()
	got, err := idx.Search([]string{"new"}, 10, idx.Version())
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestReplayDeterminism(t *testing.T) {
	build := func() []Result {
		idx := NewIndex()
		_, _ = idx.Add("a", []string{"x", "y"})
		_, _ = idx.Add("b", []string{"x"})
		_, _ = idx.Update("a", []string{"y"})
		_, _ = idx.Compact(2)
		got, _ := idx.Search([]string{"x"}, 10, 3)
		return got
	}
	first := build()
	for i := 0; i < 10; i++ {
		if got := build(); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("replay mismatch: %v != %v", got, first)
		}
	}
}
