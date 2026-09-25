package hist

import (
	"fmt"
	"testing"
)

func versionsOf(h *History) []int64 {
	es := h.All()
	vs := make([]int64, len(es))
	for i, e := range es {
		vs[i] = e.Version
	}
	return vs
}

func eqInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSixPutTable(t *testing.T) {
	want := [][]int64{{1}, {1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}}
	wantMoves := []int{0, 0, 0, 1, 1, 1}
	h := New(3)
	for i := 0; i < 6; i++ {
		v := int64(i + 1)
		h.Append(v, fmt.Sprintf("%c", 'a'+i))
		if got := versionsOf(h); !eqInts(got, want[i]) {
			t.Fatalf("step %d: retained %v, want %v", i+1, got, want[i])
		}
		if h.lastCleanupMoves != wantMoves[i] {
			t.Fatalf("step %d: cleanupMoves=%d, want %d", i+1, h.lastCleanupMoves, wantMoves[i])
		}
	}
}

func TestRetentionWindow(t *testing.T) {
	cases := []struct{ k, m int }{
		{1, 1}, {1, 50}, {3, 2}, {3, 7}, {5, 5}, {5, 100},
	}
	for _, c := range cases {
		h := New(c.k)
		for v := 1; v <= c.m; v++ {
			h.Append(int64(v), "x")
			minV := int64(1)
			if v > c.k {
				minV = int64(v - c.k + 1)
			}
			wantN := c.k
			if v < c.k {
				wantN = v
			}
			if h.Len() != wantN || h.OldestVersion() != minV || h.MaxVersion() != int64(v) {
				t.Fatalf("k=%d m=%d: got n=%d min=%d max=%d", c.k, v, h.Len(), h.OldestVersion(), h.MaxVersion())
			}
		}
	}
}

func TestGetAtVisibility(t *testing.T) {
	h := New(3)
	for v := 1; v <= 6; v++ {
		h.Append(int64(v), fmt.Sprintf("v%d", v))
	}
	cases := []struct {
		v      int64
		hit    bool
		reason string
	}{
		{0, false, "non-positive"}, {-1, false, "negative"},
		{1, false, "cleaned"}, {2, false, "cleaned"}, {3, false, "cleaned"},
		{4, true, "oldest retained"}, {5, true, "middle"}, {6, true, "latest"},
		{7, false, "future"}, {100, false, "future"},
	}
	for _, c := range cases {
		if val, ok := h.At(c.v); ok != c.hit || (ok && val != fmt.Sprintf("v%d", c.v)) {
			t.Fatalf("At(%d) %s: got %q,%v want hit=%v", c.v, c.reason, val, ok, c.hit)
		}
	}
}

// TestPutCleanupMovesBounded pins O(1) eager cleanup: the per-Put work of
// the cleanup path must stay <=1 regardless of history length.
func TestPutCleanupMovesBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := New(5)
		for v := 1; v <= m; v++ {
			h.Append(int64(v), "x")
			if h.lastCleanupMoves > 1 {
				t.Fatalf("m=%d v=%d: cleanup moves=%d, want <=1", m, v, h.lastCleanupMoves)
			}
		}
	}
}
