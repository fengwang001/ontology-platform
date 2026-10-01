package bm25

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func must(t *testing.T, err error, where string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
}

func mustSearch(t *testing.T, r *Ranker, query []string, k, asOf int) []Result {
	t.Helper()
	results, err := r.Search(query, k, asOf)
	if err != nil {
		t.Fatalf("search v%d: %v", asOf, err)
	}
	return results
}

func requireResults(t *testing.T, got, want []Result, where string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", where, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", where, got, want)
		}
	}
}

func repeatTerms(term string, count int) []string {
	terms := make([]string, count)
	for i := range terms {
		terms[i] = term
	}
	return terms
}

func scoreOf(t *testing.T, results []Result, docID string) *big.Rat {
	t.Helper()
	for _, result := range results {
		if result.DocID == docID {
			score, ok := new(big.Rat).SetString(result.Score)
			if !ok {
				t.Fatalf("invalid score %q", result.Score)
			}
			return score
		}
	}
	t.Fatalf("document %q not found in %v", docID, results)
	return nil
}

func TestSpecExample(t *testing.T) {
	r := NewRanker()
	must(t, r.Add("a", []string{"x", "y"}), "add a")
	must(t, r.Add("b", []string{"x"}), "add b")
	must(t, r.Update("a", []string{"y"}), "update a")
	must(t, r.Delete("b"), "delete b")

	requireResults(t, mustSearch(t, r, []string{"x"}, 10, 2), []Result{
		{DocID: "b", Score: "4/17"},
		{DocID: "a", Score: "4/23"},
	}, "version 2")
	requireResults(t, mustSearch(t, r, []string{"x"}, 10, 3), []Result{
		{DocID: "b", Score: "1/1"},
	}, "version 3")
	requireResults(t, mustSearch(t, r, []string{"x"}, 10, 4), nil, "version 4")
}

func TestExactFormulaCases(t *testing.T) {
	r := NewRanker()
	must(t, r.Add("d", []string{"z", "z"}), "add")
	requireResults(t, mustSearch(t, r, []string{"z", "z"}, 10, 1),
		[]Result{{DocID: "d", Score: "10/21"}}, "df=N, tf=2, dl=avgdl")

	r = NewRanker()
	must(t, r.Add("d", []string{"z"}), "single term")
	requireResults(t, mustSearch(t, r, []string{"z"}, 10, 1),
		[]Result{{DocID: "d", Score: "1/3"}}, "df=N, tf=1, dl=avgdl")

	r = NewRanker()
	must(t, r.Add("a", []string{"q", "s"}), "a average length")
	must(t, r.Add("b", []string{"q", "t"}), "b average length")
	results := mustSearch(t, r, []string{"q"}, 10, 2)
	requireResults(t, results, []Result{
		{DocID: "a", Score: "1/5"},
		{DocID: "b", Score: "1/5"},
	}, "dl=avgdl length normalization is one")

	r = NewRanker()
	must(t, r.Add("a", []string{"q"}), "short")
	before := mustSearch(t, r, []string{"q"}, 10, 1)
	must(t, r.Add("b", repeatTerms("n", 9)), "long")
	after := mustSearch(t, r, []string{"q"}, 10, 2)
	if before[0].Score == after[0].Score {
		t.Fatalf("expected score to change: before=%s after=%s", before[0].Score, after[0].Score)
	}

	r = NewRanker()
	must(t, r.Add("big", repeatTerms("q", 1000)), "large")
	results = mustSearch(t, r, []string{"q"}, 10, 1)
	score := scoreOf(t, results, "big")
	naive := naiveScore(snapshotFromCurrent(r, 1), "big", []string{"q"})
	if score.Cmp(naive) != 0 {
		t.Fatalf("large tf score %s, want %s", score.RatString(), naive.RatString())
	}
}

func TestTieOrderDuplicateQueryAndK(t *testing.T) {
	r := NewRanker()
	must(t, r.Add("B2", []string{"q"}), "B2")
	must(t, r.Add("A1", []string{"q"}), "A1")
	must(t, r.Add("A0", []string{"q"}), "A0")
	results := mustSearch(t, r, []string{"q", "q"}, 2, 3)
	if len(results) != 2 || results[0].DocID != "A0" || results[1].DocID != "A1" {
		t.Fatalf("tie/order/k result=%v", results)
	}
	if r.LastSearchPostingReads() != 3 {
		t.Fatalf("reads=%d, want 3", r.LastSearchPostingReads())
	}
}

func TestEmptyIndexVersionsAndRejections(t *testing.T) {
	r := NewRanker()
	results, err := r.Search([]string{"q"}, 10, 0)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty search=(%v,%v)", results, err)
	}
	must(t, r.Add("only", []string{"q"}), "add")
	must(t, r.Delete("only"), "delete")
	results = mustSearch(t, r, []string{"q"}, 10, 2)
	if len(results) != 0 {
		t.Fatalf("empty version result=%v", results)
	}
	if _, err := r.Search([]string{"q"}, 10, -1); !errors.Is(err, ErrVersionOutOfRange) {
		t.Fatalf("negative asOf err=%v", err)
	}
	if _, err := r.Search([]string{"q"}, 10, 3); !errors.Is(err, ErrVersionOutOfRange) {
		t.Fatalf("future asOf err=%v", err)
	}
	if fmt.Sprint(r.Version()) != "2" {
		t.Fatalf("version=%d", r.Version())
	}
}
