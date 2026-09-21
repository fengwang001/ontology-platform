package replica

import "testing"

// Regression: Truncate sliced entries[:from], keeping the entry at
// Index == from; the contract drops every entry with Index >= from,
// so Match() must become from-1 and Append(from) must succeed.
func TestTruncateDropsFromEntryRegression(t *testing.T) {
	r := New(9)
	for i := uint64(1); i <= 3; i++ {
		mustAppend(t, r, Entry{Index: i, Term: 1, Data: "old"})
	}
	r.Truncate(2)
	if got := r.Match(); got != 1 {
		t.Fatalf("Match after Truncate(2) = %d, want 1", got)
	}
	if _, ok := r.Get(2); ok {
		t.Fatal("entry at Index 2 must be dropped by Truncate(2)")
	}
	// The truncated slot must accept a conflicting entry immediately.
	mustAppend(t, r, Entry{Index: 2, Term: 2, Data: "new"})
	if e, ok := r.Get(2); !ok || e.Data != "new" {
		t.Fatalf("Get(2) = %+v, %v, want the conflicting entry", e, ok)
	}
}
