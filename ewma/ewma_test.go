package ewma

import "testing"

// TestValueReadsConstant proves Value reads O(1) state: after m
// updates (m from 100 to 10000), the number of historical
// observations read by the last Value call is always 1, not m.
// The counter is an unexported field, readable only inside this
// package — no exported API exposes it.
func TestValueReadsConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		e := New(0.9, 0, true)
		for i := 0; i < m; i++ {
			e.Update(float64(i%17) - 8)
		}
		_ = e.Value()
		if got := e.lastReads.Load(); got != 1 {
			t.Fatalf("m=%d: Value read %d historical observations, want 1", m, got)
		}
	}
}
