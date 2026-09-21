package replica

import "testing"

func TestAppendContiguous(t *testing.T) {
	r := New(0)
	for i := uint64(1); i <= 5; i++ {
		if err := r.Append(Entry{Index: i, Term: 1, Data: "x"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if got := r.Match(); got != 5 {
		t.Fatalf("Match() = %d, want 5", got)
	}
	e, ok := r.Get(3)
	if !ok || e.Index != 3 || e.Term != 1 {
		t.Fatalf("Get(3) = %+v, %v", e, ok)
	}
	if _, ok := r.Get(6); ok {
		t.Fatal("Get(6) should miss")
	}
	if _, ok := r.Get(0); ok {
		t.Fatal("Get(0) should miss")
	}
}

func TestAppendRejectsGapAndRewind(t *testing.T) {
	r := New(1)
	if err := r.Append(Entry{Index: 2, Term: 1}); err == nil {
		t.Fatal("gap append should fail")
	}
	if err := r.Append(Entry{Index: 0, Term: 1}); err == nil {
		t.Fatal("index 0 append should fail")
	}
	if err := r.Append(Entry{Index: 1, Term: 2}); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(Entry{Index: 1, Term: 2}); err == nil {
		t.Fatal("duplicate index should fail")
	}
	if err := r.Append(Entry{Index: 2, Term: 1}); err == nil {
		t.Fatal("term regression should fail")
	}
	if got := r.Match(); got != 1 {
		t.Fatalf("Match() = %d, want 1 after rejects", got)
	}
}

func TestTruncateAndReappend(t *testing.T) {
	r := New(2)
	for i := uint64(1); i <= 4; i++ {
		if err := r.Append(Entry{Index: i, Term: 1, Data: "old"}); err != nil {
			t.Fatal(err)
		}
	}
	r.Truncate(3)
	if got := r.Match(); got != 2 {
		t.Fatalf("Match() = %d, want 2 after truncate", got)
	}
	if _, ok := r.Get(3); ok {
		t.Fatal("entry 3 should be gone")
	}
	for i := uint64(3); i <= 5; i++ {
		if err := r.Append(Entry{Index: i, Term: 2, Data: "new"}); err != nil {
			t.Fatalf("re-append %d: %v", i, err)
		}
	}
	if got := r.Match(); got != 5 {
		t.Fatalf("Match() = %d, want 5", got)
	}
	for i := uint64(1); i <= 5; i++ {
		e, ok := r.Get(i)
		if !ok || e.Index != i {
			t.Fatalf("Get(%d) = %+v, %v", i, e, ok)
		}
	}
	r.Truncate(10) // beyond match: no-op
	if got := r.Match(); got != 5 {
		t.Fatalf("Match() = %d, want 5 after no-op truncate", got)
	}
	r.Truncate(0) // truncates everything
	if got := r.Match(); got != 0 {
		t.Fatalf("Match() = %d, want 0 after full truncate", got)
	}
}
