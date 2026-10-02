package expander

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustIndex(t *testing.T, m *Merger, docID string, terms []string) {
	t.Helper()
	if err := m.Index(docID, terms); err != nil {
		t.Fatalf("Index(%q, %v) unexpected error: %v", docID, terms, err)
	}
}

func mustRemove(t *testing.T, m *Merger, docID string) {
	t.Helper()
	if err := m.Remove(docID); err != nil {
		t.Fatalf("Remove(%q) unexpected error: %v", docID, err)
	}
}

func mustPick(t *testing.T, m *Merger, term string) {
	t.Helper()
	if err := m.Pick(term); err != nil {
		t.Fatalf("Pick(%q) unexpected error: %v", term, err)
	}
}

func mustExpand(t *testing.T, m *Merger, query string, maxExp int) Result {
	t.Helper()
	r, err := m.Expand(query, maxExp)
	if err != nil {
		t.Fatalf("Expand(%q, %d) unexpected error: %v", query, maxExp, err)
	}
	return r
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func checkResult(t *testing.T, r Result, cands []Candidate, truncated bool, docs []string) {
	t.Helper()
	if len(r.Candidates) != len(cands) {
		t.Fatalf("candidates = %v, want %v", r.Candidates, cands)
	}
	for i, c := range cands {
		if r.Candidates[i] != c {
			t.Fatalf("candidates[%d] = %+v, want %+v (all: %v)", i, r.Candidates[i], c, r.Candidates)
		}
	}
	if r.Truncated != truncated {
		t.Fatalf("Truncated = %v, want %v", r.Truncated, truncated)
	}
	if len(r.Docs) != len(docs) {
		t.Fatalf("Docs = %v, want %v", r.Docs, docs)
	}
	for i, d := range docs {
		if r.Docs[i] != d {
			t.Fatalf("Docs = %v, want %v", r.Docs, docs)
		}
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	m := New()
	mustIndex(t, m, "d1", []string{"apple", "apply", "ape"})
	mustIndex(t, m, "d2", []string{"apple", "apply"})
	mustIndex(t, m, "d3", []string{"apple", "apex"})

	// Expand #1 (t=1): no picks, plain conditional df order.
	r := mustExpand(t, m, "ap", 2)
	checkResult(t, r,
		[]Candidate{{"apple", 3, 3}, {"apply", 2, 2}},
		true, []string{"d1", "d2", "d3"})

	// Two picks of ape at c=1 collapse into a single record.
	mustPick(t, m, "ape")
	mustPick(t, m, "ape")

	// Expand #2 (t=2): ape gets 1+2=3, ties apple, byte order wins.
	r = mustExpand(t, m, "ap", 2)
	checkResult(t, r,
		[]Candidate{{"ape", 1, 3}, {"apple", 3, 3}},
		true, []string{"d1", "d2", "d3"})

	mustPick(t, m, "apex") // c=2

	// Expand #3 (t=3): ape 3, apex 1+2=3, apple 3; byte order.
	r = mustExpand(t, m, "*2", 2)
	_ = r
}
