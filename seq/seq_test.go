package seq

import "testing"

// A duplicate (seq far below next) must be rejected by an O(1)
// comparison against next: the number of inspected entries must not
// grow with the delivered log size m.
func TestDedupCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		tr := New()
		for i := 0; i < m; i++ {
			tr.Add(int64(i), i)
		}
		tr.Add(0, "dup") // duplicate, seq far below next=m
		if tr.checked != 1 {
			t.Fatalf("m=%d: duplicate check inspected %d entries, want 1", m, tr.checked)
		}
		if tr.Dups() != 1 {
			t.Fatalf("m=%d: dups=%d, want 1", m, tr.Dups())
		}
		if got := len(tr.Log()); got != m {
			t.Fatalf("m=%d: delivered=%d, want %d", m, got, m)
		}
	}
}
