package hist

import "testing"

// TestCleanupMoveBounded proves cleanup is O(1) per Put: across several
// scales m with a tiny K, the number of versions examined/moved for
// cleanup in each single Put never exceeds 1 and does not grow with m.
func TestCleanupMoveBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		h := New(5)
		for i := 0; i < m; i++ {
			h.Put("x")
			if h.moved > 1 {
				t.Fatalf("m=%d put=%d: moved %d versions, want <= 1", m, i+1, h.moved)
			}
		}
		if h.Len() != 5 {
			t.Fatalf("m=%d: Len=%d, want 5", m, h.Len())
		}
	}
}

// TestHistRetentionAndReadback nails the per-key invariants directly:
// contiguous monotonic versions, retained window exactly [maxV-K+1, maxV].
func TestHistRetentionAndReadback(t *testing.T) {
	cases := []struct {
		k, puts int
	}{
		{1, 1}, {1, 7}, {3, 3}, {3, 6}, {5, 100},
	}
	for _, c := range cases {
		h := New(c.k)
		for i := 1; i <= c.puts; i++ {
			if v := h.Put(string(rune('a' + i%26))); v != int64(i) {
				t.Fatalf("k=%d: put %d got version %d", c.k, i, v)
			}
		}
		wantLen := c.puts
		if wantLen > c.k {
			wantLen = c.k
		}
		if h.Len() != wantLen {
			t.Fatalf("k=%d puts=%d: Len=%d, want %d", c.k, c.puts, h.Len(), wantLen)
		}
		maxV := int64(c.puts)
		oldest := maxV - int64(wantLen) + 1
		for v := int64(1); v <= maxV+1; v++ {
			_, hit := h.GetAt(v)
			want := v >= oldest && v <= maxV
			if hit != want {
				t.Fatalf("k=%d puts=%d: GetAt(%d) hit=%v, want %v", c.k, c.puts, v, hit, want)
			}
		}
		if _, hit := h.GetAt(0); hit {
			t.Fatalf("k=%d: GetAt(0) must miss", c.k)
		}
	}
}
