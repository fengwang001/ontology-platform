package replica

import (
	"errors"
	"testing"
)

// Semantics 4: Append rejects gaps and regressions; Truncate rolls back
// a conflicting tail and re-appending stays contiguous, with Match
// always reflecting the persisted content.
func TestAppendContiguity(t *testing.T) {
	r := New(0)
	if err := r.Append(Entry{Index: 2, Term: 1, Data: "skip"}); err == nil {
		t.Fatal("append with gap must fail")
	}
	if err := r.Append(Entry{Index: 1, Term: 2, Data: "a"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := r.Append(Entry{Index: 1, Term: 2, Data: "dup"}); err == nil {
		t.Fatal("re-append of same index must fail")
	}
	if err := r.Append(Entry{Index: 2, Term: 2, Data: "b"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if got := r.Match(); got != 2 {
		t.Fatalf("Match = %d, want 2", got)
	}
}

func TestAppendTermRegression(t *testing.T) {
	r := New(0)
	mustAppend(t, r, Entry{Index: 1, Term: 5, Data: "a"})
	err := r.Append(Entry{Index: 2, Term: 4, Data: "b"})
	if !errors.Is(err, ErrTermRegression) {
		t.Fatalf("term regression: got %v", err)
	}
	if err := r.Append(Entry{Index: 2, Term: 5, Data: "b"}); err != nil {
		t.Fatalf("equal term must be accepted: %v", err)
	}
}

func TestTruncateThenReappend(t *testing.T) {
	r := New(0)
	for i := uint64(1); i <= 5; i++ {
		mustAppend(t, r, Entry{Index: i, Term: 1, Data: "old"})
	}
	r.Truncate(3)
	if got := r.Match(); got != 2 {
		t.Fatalf("Match after truncate = %d, want 2", got)
	}
	if _, ok := r.Get(3); ok {
		t.Fatal("index 3 must be gone after truncate")
	}
	// Re-append a conflicting tail at a higher term.
	for i := uint64(3); i <= 6; i++ {
		mustAppend(t, r, Entry{Index: i, Term: 2, Data: "new"})
	}
	if got := r.Match(); got != 6 {
		t.Fatalf("Match = %d, want 6", got)
	}
	for i := uint64(1); i <= 6; i++ {
		e, ok := r.Get(i)
		if !ok {
			t.Fatalf("index %d missing", i)
		}
		want := "old"
		if i >= 3 {
			want = "new"
		}
		if e.Data != want {
			t.Fatalf("index %d data = %q, want %q", i, e.Data, want)
		}
	}
}

func TestTruncateEdgeCases(t *testing.T) {
	r := New(0)
	r.Truncate(1) // no-op on empty log
	mustAppend(t, r, Entry{Index: 1, Term: 1, Data: "a"})
	r.Truncate(0) // treated as 1: drops everything
	if got := r.Match(); got != 0 {
		t.Fatalf("Match = %d, want 0", got)
	}
	r.Truncate(10) // beyond end: no-op
	mustAppend(t, r, Entry{Index: 1, Term: 1, Data: "b"})
	if e, ok := r.Get(1); !ok || e.Data != "b" {
		t.Fatalf("got %v %v", e, ok)
	}
}

func mustAppend(t *testing.T, r *Replica, e Entry) {
	t.Helper()
	if err := r.Append(e); err != nil {
		t.Fatalf("append %+v: %v", e, err)
	}
}
