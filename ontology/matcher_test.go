package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func mustTerms(t *testing.T, values ...string) []string {
	t.Helper()
	return values
}

func addDocs(t *testing.T, m *Matcher, docs map[string][]string) {
	t.Helper()
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := m.Add(id, docs[id]); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}
}

func TestSpecExamples(t *testing.T) {
	tests := []struct {
		name   string
		doc    string
		terms  []string
		query  []string
		slop   int
		maxGap int
		want   *Match
	}{
		{"nearest middle fails max-gap 2", "d1", mustTerms(t, "a", "b", "x", "b", "x", "c"), []string{"a", "b", "c"}, 3, 2, &Match{"d1", 1, 3}},
		{"all step gaps fail max-gap 1", "d1", mustTerms(t, "a", "b", "x", "b", "x", "c"), []string{"a", "b", "c"}, 3, 1, nil},
		{"slop permits max-gap 3", "d1", mustTerms(t, "a", "b", "x", "b", "x", "c"), []string{"a", "b", "c"}, 3, 3, &Match{"d1", 1, 3}},
		{"sentence exact slop zero", "d2", mustTerms(t, "a", "b", sentenceBoundary, "a", "b", "a", "x", "b"), []string{"a", "b"}, 0, 100, &Match{"d2", 2, 0}},
		{"sentence slop one", "d2", mustTerms(t, "a", "b", sentenceBoundary, "a", "b", "a", "x", "b"), []string{"a", "b"}, 1, 100, &Match{"d2", 3, 0}},
		{"reverse cannot cross sentence", "d2", mustTerms(t, "a", "b", sentenceBoundary, "a", "b", "a", "x", "b"), []string{"b", "a"}, 2, 100, &Match{"d2", 1, 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMatcher()
			if err := m.Add(tc.doc, tc.terms); err != nil {
				t.Fatal(err)
			}
			got, err := m.Phrase(tc.query, tc.slop, tc.maxGap, 10)
			if err != nil {
				t.Fatalf("Phrase: %v", err)
			}
			want := []Match{}
			if tc.want != nil {
				want = []Match{*tc.want}
			}
			if len(got) != len(want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			if len(want) == 1 && got[0] != want[0] {
				t.Fatalf("got %+v, want %+v", got[0], want[0])
			}
		})
	}
}

func TestBoundaryRules(t *testing.T) {
	tests := []struct {
		name   string
		terms  []string
		query  []string
		slop   int
		maxGap int
		freq   int
		minGap int
	}{
		{"strict adjacent", []string{"a", "b", "c"}, []string{"a", "b", "c"}, 0, 0, 1, 0},
		{"total gap exactly slop", []string{"a", "x", "b", "c"}, []string{"a", "b", "c"}, 1, 1, 1, 1},
		{"total gap one over slop", []string{"a", "x", "x", "b"}, []string{"a", "b"}, 1, 2, 0, 0},
		{"step gap exactly max gap", []string{"a", "x", "b"}, []string{"a", "b"}, 1, 1, 1, 1},
		{"step gap one over max gap", []string{"a", "x", "x", "b"}, []string{"a", "b"}, 2, 1, 0, 0},
		{"farther middle is legal", []string{"a", "b", "x", "b", "x", "c"}, []string{"a", "b", "c"}, 3, 2, 1, 3},
		{"boundary immediately outside", []string{"a", "x", "b", sentenceBoundary}, []string{"a", "b"}, 1, 1, 1, 1},
		{"boundary inside range", []string{"a", sentenceBoundary, "b"}, []string{"a", "b"}, 100, 100, 0, 0},
		{"repeated words exact", []string{"a", "a", "a"}, []string{"a", "a"}, 0, 0, 2, 0},
		{"reversed order fails", []string{"b", "a"}, []string{"a", "b"}, 100, 100, 0, 0},
		{"one start counted once", []string{"a", "b", "c", "b", "c"}, []string{"a", "b", "c"}, 3, 3, 1, 0},
		{"overlapping distinct starts", []string{"a", "a", "a", "a"}, []string{"a", "a"}, 1, 1, 3, 0},
		{"single term frequency", []string{"a", "x", "a", sentenceBoundary, "a"}, []string{"a"}, 0, 0, 3, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMatcher()
			if err := m.Add("doc", tc.terms); err != nil {
				t.Fatal(err)
			}
			got, err := m.Phrase(tc.query, tc.slop, tc.maxGap, 10)
			if err != nil {
				t.Fatal(err)
			}
			if tc.freq == 0 {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no matches", got)
				}
				return
			}
			if len(got) != 1 || got[0].Freq != tc.freq || got[0].MinGap != tc.minGap {
				t.Fatalf("got %+v, want freq=%d minGap=%d", got, tc.freq, tc.minGap)
			}
		})
	}
}

func TestOrderingAndLimit(t *testing.T) {
	m := NewMatcher()
	addDocs(t, m, map[string][]string{
		"high": mustTerms(t, "a", "b", "a", "b"),
		"z":    mustTerms(t, "a", "x", "b"),
		"a":    mustTerms(t, "a", "x", "b"),
	})

	got, err := m.Phrase([]string{"a", "b"}, 1, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{DocID: "high", Freq: 2, MinGap: 0},
		{DocID: "a", Freq: 1, MinGap: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDeleteAndReadd(t *testing.T) {
	m := NewMatcher()
	if err := m.Add("doc", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("doc"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("doc"); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("delete missing got %v, want %v", err, ErrDocNotFound)
	}
	if err := m.Add("doc", []string{"x", "y"}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Phrase([]string{"a", "b"}, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("deleted document still matched: %+v", got)
	}
}

func TestRejectionOrderLeavesStateUntouched(t *testing.T) {
	m := NewMatcher()
	if err := m.Add("existing", []string{"x"}); err != nil {
		t.Fatal(err)
	}

	if err := m.Add("", []string{"x"}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("got %v", err)
	}
	if err := m.Add("bad", nil); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("got %v", err)
	}
	if err := m.Add("bad", []string{"a", ""}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("got %v", err)
	}
	if err := m.Add("existing", []string{"y"}); !errors.Is(err, ErrDuplicateDocID) {
		t.Fatalf("got %v", err)
	}
	_, err := m.Phrase(nil, 0, 0, 1)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("got %v", err)
	}
	longQuery := []string{"a", "a", "a", "a", "a", "a", "a", "a", "a"}
	_, err = m.Phrase(longQuery, 0, 0, 1)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", ""}, 0, 0, 1)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", sentenceBoundary}, 0, 0, 1)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", "b"}, 101, 0, 1)
	if !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", "b"}, -1, 0, 1)
	if !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", "b"}, 0, 101, 1)
	if !errors.Is(err, ErrInvalidGap) {
		t.Fatalf("got %v", err)
	}
	_, err = m.Phrase([]string{"a", "b"}, 0, 0, 0)
	if !errors.Is(err, ErrInvalidK) {
		t.Fatalf("got %v", err)
	}

	got, err := m.Phrase([]string{"missing"}, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing terms should return empty result: %+v", got)
	}
	got, err = m.Phrase([]string{"x"}, 0, 0, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("state changed by rejected operation: got=%+v err=%v", got, err)
	}
}

func TestConcurrentOperationsAndDeterminism(t *testing.T) {
	m := NewMatcher()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			id := fmt.Sprintf("doc-%d", worker)
			_ = m.Add(id, []string{"a", "b", "x", "b"})
			_, _ = m.Phrase([]string{"a", "b", "b"}, 3, 3, 10)
			if worker%2 == 0 {
				_ = m.Delete(id)
			}
		}(worker)
	}
	wg.Wait()

	first, err := m.Phrase([]string{"a", "b", "b"}, 3, 3, 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Phrase([]string{"a", "b", "b"}, 3, 3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("non-deterministic results: %+v vs %+v", first, second)
	}
}

func naiveMatch(terms, query []string, slop, maxGap int) (int, int) {
	postings := make([][]int, len(query))
	var boundaries []int
	for pos, term := range terms {
		if term == sentenceBoundary {
			boundaries = append(boundaries, pos)
			continue
		}
		for i, queryTerm := range query {
			if term == queryTerm {
				postings[i] = append(postings[i], pos)
			}
		}
	}

	validStarts := make(map[int]int)
	if len(query) == 1 {
		for _, pos := range postings[0] {
			validStarts[pos] = 0
		}
	} else {
		var dfs func(layer int, start, prev, sentenceEnd int)
		dfs = func(layer int, start, prev, sentenceEnd int) {
			if layer == len(query) {
				gap := prev - start - (len(query) - 1)
				if gap <= slop {
					oldGap, exists := validStarts[start]
					if !exists || gap < oldGap {
						validStarts[start] = gap
					}
				}
				return
			}

			low := sort.SearchInts(postings[layer], prev+1)
			lastAllowed := prev + maxGap + 1
			if lastAllowed >= sentenceEnd {
				lastAllowed = sentenceEnd - 1
			}
			high := sort.SearchInts(postings[layer], lastAllowed+1)
			for _, next := range postings[layer][low:high] {
				dfs(layer+1, start, next, sentenceEnd)
			}
		}

		for _, start := range postings[0] {
			boundaryAt := sort.SearchInts(boundaries, start)
			sentenceEnd := len(terms)
			if boundaryAt < len(boundaries) {
				sentenceEnd = boundaries[boundaryAt]
			}
			dfs(1, start, start, sentenceEnd)
		}
	}

	freq := len(validStarts)
	if freq == 0 {
		return 0, 0
	}
	minGap := 0
	first := true
	for _, gap := range validStarts {
		if first || gap < minGap {
			minGap = gap
		}
		first = false
	}
	return freq, minGap
}

func naivePhrase(docs map[string][]string, query []string, slop, maxGap, k int) []Match {
	results := make([]Match, 0)
	for docID, terms := range docs {
		freq, minGap := naiveMatch(terms, query, slop, maxGap)
		if freq > 0 {
			results = append(results, Match{DocID: docID, Freq: freq, MinGap: minGap})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Freq != results[j].Freq {
			return results[i].Freq > results[j].Freq
		}
		if results[i].MinGap != results[j].MinGap {
			return results[i].MinGap < results[j].MinGap
		}
		return results[i].DocID < results[j].DocID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	termPool := []string{"a", "b", "c", "x", sentenceBoundary}

	for iteration := 0; iteration < 2000; iteration++ {
		docCount := 1 + rng.Intn(10)
		docs := make(map[string][]string, docCount)
		m := NewMatcher()
		for docIndex := 0; docIndex < docCount; docIndex++ {
			id := fmt.Sprintf("doc-%02d", docIndex)
			termCount := 1 + rng.Intn(12)
			terms := make([]string, termCount)
			for i := range terms {
				terms[i] = termPool[rng.Intn(len(termPool))]
			}
			docs[id] = terms
			if err := m.Add(id, terms); err != nil {
				t.Fatal(err)
			}
		}

		queryLen := 1 + rng.Intn(8)
		query := make([]string, queryLen)
		for i := range query {
			query[i] = []string{"a", "b", "c"}[rng.Intn(3)]
		}
		slop := rng.Intn(6)
		maxGap := rng.Intn(6)
		k := 1 + rng.Intn(8)

		got, err := m.Phrase(query, slop, maxGap, k)
		if err != nil {
			t.Fatal(err)
		}
		want := naivePhrase(docs, query, slop, maxGap, k)
		basis := "naive backtracking per distinct start; strict order, per-step gap, total gap, and sentence end"
		t.Logf("input #%d docs=%s query=%s slop=%d maxGap=%d k=%d\noutput=%v\nbasis=%s",
			iteration, formatDocs(docs), strings.Join(query, ","), slop, maxGap, k, got, basis)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("case %d mismatch:\ngot=%v\nwant=%v\nquery=%v slop=%d maxGap=%d k=%d\ndocs=%s",
				iteration, got, want, query, slop, maxGap, k, formatDocs(docs))
		}
	}
}

func formatDocs(docs map[string][]string) string {
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, id+"=["+strings.Join(docs[id], ",")+"]")
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

func TestReadPostingItemsIndependentOfIrrelevantDocuments(t *testing.T) {
	countsAtSizes := make(map[int]uint64)
	for _, docCount := range []int{1000, 100000} {
		m := NewMatcher()
		if err := m.Add("target", []string{"a", "b", "c"}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < docCount-1; i++ {
			id := fmt.Sprintf("irrelevant-%06d", i)
			if err := m.Add(id, []string{"z"}); err != nil {
				t.Fatal(err)
			}
		}
		m.resetReadPostingItems()
		got, err := m.Phrase([]string{"a", "b", "c"}, 0, 0, 10)
		if err != nil || len(got) != 1 {
			t.Fatalf("got=%v err=%v", got, err)
		}
		count := m.readPostingItems()
		countsAtSizes[docCount] = count
		if count != 3 {
			t.Fatalf("size %d read %d posting items, want 3", docCount, count)
		}
	}
	if countsAtSizes[1000] != countsAtSizes[100000] {
		t.Fatalf("posting reads grew with irrelevant documents: %v", countsAtSizes)
	}
}

func TestReadPostingItemsDeduplicatesRepeatedQueryTerms(t *testing.T) {
	m := NewMatcher()
	if err := m.Add("target", []string{"a", "a", "a", "b", "b"}); err != nil {
		t.Fatal(err)
	}
	m.resetReadPostingItems()
	if _, err := m.Phrase([]string{"a", "a", "b", "a"}, 10, 10, 10); err != nil {
		t.Fatal(err)
	}
	if got, want := m.readPostingItems(), uint64(5); got != want {
		t.Fatalf("read %d posting items, want %d (sum over deduplicated terms)", got, want)
	}
}
