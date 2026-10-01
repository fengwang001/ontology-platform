package bm25

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestRejectionOrderAndNoVersionConsumption(t *testing.T) {
	r := NewRanker()
	cases := []struct {
		name            string
		call            func() error
		want            error
		consumesVersion bool
	}{
		{"add empty doc", func() error { return r.Add("", []string{"q"}) }, ErrInvalidArgument, false},
		{"add nil terms", func() error { return r.Add("d", nil) }, ErrInvalidArgument, false},
		{"add blank term", func() error { return r.Add("d", []string{""}) }, ErrInvalidArgument, false},
		{"first valid add", func() error { return r.Add("d", []string{"q"}) }, nil, true},
		{"duplicate", func() error { return r.Add("d", []string{"q"}) }, ErrDuplicateDocument, false},
		{"update invalid", func() error { return r.Update("d", nil) }, ErrInvalidArgument, false},
		{"update missing", func() error { return r.Update("missing", []string{"q"}) }, ErrDocumentNotFound, false},
		{"delete invalid", func() error { return r.Delete("") }, ErrInvalidArgument, false},
		{"delete missing", func() error { return r.Delete("missing") }, ErrDocumentNotFound, false},
	}

	for _, tc := range cases {
		before := r.Version()
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v want=%v", tc.name, err, tc.want)
		}
		if tc.consumesVersion && r.Version() != before+1 {
			t.Fatalf("%s should consume one version", tc.name)
		}
		if !tc.consumesVersion && r.Version() != before {
			t.Fatalf("%s changed version", tc.name)
		}
	}

	searchCases := []struct {
		name  string
		query []string
		k     int
		asOf  int
		want  error
	}{
		{"empty query", nil, 10, 1, ErrInvalidArgument},
		{"blank term", []string{""}, 10, 1, ErrInvalidArgument},
		{"bad k", []string{"q"}, 0, 1, ErrInvalidArgument},
		{"negative", []string{"q"}, 10, -1, ErrVersionOutOfRange},
		{"future", []string{"q"}, 10, 2, ErrVersionOutOfRange},
	}
	for _, tc := range searchCases {
		_, err := r.Search(tc.query, tc.k, tc.asOf)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v want=%v", tc.name, err, tc.want)
		}
	}
}

func TestCompactRulesAndPreservation(t *testing.T) {
	r := buildScenario()
	query := []string{"q", "x"}
	before := map[int][]Result{}
	for version := 3; version <= r.Version(); version++ {
		before[version] = mustSearch(t, r, query, 100, version)
	}

	if err := r.Compact(7); !errors.Is(err, ErrVersionOutOfRange) {
		t.Fatalf("keep V+1 err=%v", err)
	}
	must(t, r.Compact(3), "compact to 3")
	if r.Watermark() != 3 || r.Version() != 6 {
		t.Fatalf("version/watermark=(%d,%d)", r.Version(), r.Watermark())
	}
	must(t, r.Compact(3), "same watermark")
	if err := r.Compact(2); !errors.Is(err, ErrWatermarkRollback) {
		t.Fatalf("rollback err=%v", err)
	}
	if _, err := r.Search(query, 100, 2); !errors.Is(err, ErrVersionCompacted) {
		t.Fatalf("W-1 err=%v", err)
	}
	if _, err := r.Search(query, 100, -1); !errors.Is(err, ErrVersionOutOfRange) {
		t.Fatalf("negative beats compacted err=%v", err)
	}

	for version := 3; version <= r.Version(); version++ {
		after := mustSearch(t, r, query, 100, version)
		if fmt.Sprint(after) != fmt.Sprint(before[version]) {
			t.Fatalf("version %d changed: before=%v after=%v", version, before[version], after)
		}
	}
}

func TestCurrentReadsDoNotGrowWithIrrelevantDocuments(t *testing.T) {
	build := func(irrelevant int) (*Ranker, int) {
		r := NewRanker()
		matchCount := 10
		for i := 0; i < matchCount; i++ {
			must(t, r.Add(fmt.Sprintf("match-%03d", i), []string{"needle"}), "match add")
		}
		for i := 0; i < irrelevant; i++ {
			must(t, r.Add(fmt.Sprintf("other-%06d", i), []string{"noise"}), "other add")
		}
		_, err := r.Search([]string{"needle", "needle"}, 100, r.Version())
		if err != nil {
			t.Fatal(err)
		}
		return r, r.LastSearchPostingReads()
	}

	_, reads1000 := build(1000)
	_, reads100000 := build(100000)
	if reads1000 != 10 || reads100000 != 10 {
		t.Fatalf("reads at 1000=%d and 100000=%d, both want 10", reads1000, reads100000)
	}
	t.Logf("posting reads: 1000 total docs=%d, 100000 total docs=%d; both read 10 current postings", reads1000, reads100000)
}

func TestConcurrentUpdatesAreAtomic(t *testing.T) {
	r := NewRanker()
	must(t, r.Add("doc", []string{"old"}), "seed")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				term := fmt.Sprintf("w%d", worker)
				_ = r.Update("doc", []string{term, term})
				results, err := r.Search([]string{term}, 10, r.Version())
				if err != nil {
					t.Error(err)
					return
				}
				if len(results) > 0 && results[0].DocID != "doc" {
					t.Errorf("observed unexpected document %v", results)
				}
			}
		}(worker)
	}
	wg.Wait()
}

func buildScenario() *Ranker {
	r := NewRanker()
	_ = r.Add("a", []string{"q", "x"})
	_ = r.Add("b", []string{"q", "q"})
	_ = r.Update("a", []string{"q", "q", "z"})
	_ = r.Delete("b")
	_ = r.Add("c", []string{"x"})
	_ = r.Update("c", []string{"q", "x", "x"})
	return r
}
