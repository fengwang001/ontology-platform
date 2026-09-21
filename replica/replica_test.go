package replica

import "testing"

func TestAppendContiguous(t *testing.T) {
	r := New(1)
	if err := r.Append(Entry{Index: 1, Term: 1, Data: "a"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := r.Append(Entry{Index: 3, Term: 1, Data: "gap"}); err == nil {
		t.Fatal("expected gap rejection")
	}
	if err := r.Append(Entry{Index: 1, Term: 1, Data: "dup"}); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	if err := r.Append(Entry{Index: 2, Term: 2, Data: "b"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if got := r.Match(); got != 2 {
		t.Fatalf("Match = %d, want 2", got)
	}
}

func TestAppendTermRegression(t *testing.T) {
	r := New(2)
	mustAppend(t, r, Entry{Index: 1, Term: 5, Data: "a"})
	if err := r.Append(Entry{Index: 2, Term: 4, Data: "b"}); err == nil {
		t.Fatal("expected term regression rejection")
	}
	if err := r.Append(Entry{Index: 2, Term: 5, Data: "b"}); err != nil {
		t.Fatalf("equal term should be accepted: %v", err)
	}
}

func TestTruncateThenReappend(t *testing.T) {
	r := New(3)
	for i := uint64(1); i <= 4; i++ {
		mustAppend(t, r, Entry{Index: i, Term: i, Data: "old"})
	}
	r.Truncate(3)
	if got := r.Match(); got != 2 {
		t.Fatalf("Match after truncate = %d, want 2", got)
	}
	if _, ok := r.Get(3); ok {
		t.Fatal("index 3 should be gone after truncate")
	}
	// Re-append conflicting entries; continuity must still hold.
	mustAppend(t, r, Entry{Index: 3, Term: 9, Data: "new-3"})
	mustAppend(t, r, Entry{Index: 4, Term: 9, Data: "new-4"})
	if err := r.Append(Entry{Index: 6, Term: 9, Data: "gap"}); err == nil {
		t.Fatal("expected gap rejection after re-append")
	}
	e, ok := r.Get(4)
	if !ok || e.Data != "new-4" {
		t.Fatalf("Get(4) = %+v, %v", e, ok)
	}
	if got := r.Match(); got != 4 {
		t.Fatalf("Match = %d, want 4", got)
	}
}

func TestTruncateNoop(t *testing.T) {
	r := New(4)
	mustAppend(t, r, Entry{Index: 1, Term: 1, Data: "a"})
	r.Truncate(0)
	r.Truncate(5)
	if got := r.Match(); got != 1 {
		t.Fatalf("Match = %d, want 1", got)
	}
}

func TestGetBounds(t *testing.T) {
	r := New(5)
	if _, ok := r.Get(0); ok {
		t.Fatal("Get(0) should miss")
	}
	if _, ok := r.Get(1); ok {
		t.Fatal("Get(1) on empty log should miss")
	}
}

func mustAppend(t *testing.T, r *Replica, e Entry) {
	t.Helper()
	if err := r.Append(e); err != nil {
		t.Fatalf("append %+v: %v", e, err)
	}
}
