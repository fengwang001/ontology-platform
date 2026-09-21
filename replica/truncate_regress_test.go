package replica

import "testing"

// Regression: Truncate(from) must drop the entry AT `from` too
// (Index >= from), so Match() becomes from-1; the old code sliced
// [:from] and kept the conflicting entry, blocking re-append.
func TestTruncateDropsFromEntry(t *testing.T) {
	r := New(10)
	for i := uint64(1); i <= 3; i++ {
		mustAppend(t, r, Entry{Index: i, Term: 1, Data: "old"})
	}
	r.Truncate(3)
	if got := r.Match(); got != 2 {
		t.Fatalf("Match after Truncate(3) = %d, want 2", got)
	}
	if _, ok := r.Get(3); ok {
		t.Fatal("entry at `from` must be dropped, not kept")
	}
	// The exact regression: Append(from) must succeed right after.
	if err := r.Append(Entry{Index: 3, Term: 2, Data: "new"}); err != nil {
		t.Fatalf("Append(3) after Truncate(3): %v", err)
	}
	e, ok := r.Get(3)
	if !ok || e.Data != "new" {
		t.Fatalf("Get(3) = %+v, %v; want new entry", e, ok)
	}
}
