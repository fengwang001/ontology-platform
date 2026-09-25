package heap

import "testing"

func TestMinTieBreak(t *testing.T) {
	cases := []struct {
		name string
		adds []Counter
		want Counter // expected Min()
	}{
		{"distinct counts", []Counter{{Key: 9, Count: 5}, {Key: 2, Count: 1}, {Key: 7, Count: 3}}, Counter{Key: 2, Count: 1}},
		{"tie picks smaller key", []Counter{{Key: 8, Count: 2}, {Key: 3, Count: 2}, {Key: 5, Count: 2}}, Counter{Key: 3, Count: 2}},
		{"tie non-adjacent inserts", []Counter{{Key: 4, Count: 1}, {Key: 1, Count: 9}, {Key: 2, Count: 1}}, Counter{Key: 2, Count: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			for _, c := range tc.adds {
				h.Add(c)
			}
			if got := h.Min(); got != tc.want {
				t.Fatalf("Min() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIncAndReplaceMin(t *testing.T) {
	h := New()
	for _, c := range []Counter{{Key: 1, Count: 1}, {Key: 2, Count: 1}, {Key: 3, Count: 5}} {
		h.Add(c)
	}
	h.Inc(1) // (1,2) (2,1) (3,5)
	if got := h.Min(); got.Key != 2 || got.Count != 1 {
		t.Fatalf("after Inc, Min() = %+v, want key 2 count 1", got)
	}
	old := h.ReplaceMin(Counter{Key: 9, Count: 2, Err: 1})
	if old != (Counter{Key: 2, Count: 1}) {
		t.Fatalf("ReplaceMin evicted %+v, want (2,1,0)", old)
	}
	if h.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", h.Len())
	}
	if _, ok := h.Get(2); ok {
		t.Fatal("evicted key 2 still present")
	}
	if got := h.Min(); got.Key != 1 || got.Count != 2 {
		t.Fatalf("Min() = %+v, want key 1 count 2", got)
	}
}

// TestFindMinComparisonsConstant pins the complexity contract: locating
// the min-count counter during a replace is O(1), not a linear scan.
// White-box: reads the unexported counter directly, never via an
// exported method.
func TestFindMinComparisonsConstant(t *testing.T) {
	sizes := []int{100, 500, 1000, 5000, 10000}
	got := make([]int, 0, len(sizes))
	for _, k := range sizes {
		h := New()
		for i := 0; i < k; i++ {
			h.Add(Counter{Key: i, Count: i + 1}) // all counts distinct
		}
		h.ReplaceMin(Counter{Key: k, Count: k + 1})
		if h.lastCmp > 1 {
			t.Fatalf("k=%d: min-locate comparisons = %d, want <= 1 (O(1) root peek)", k, h.lastCmp)
		}
		got = append(got, h.lastCmp)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Fatalf("comparisons grow with k: %v", got)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if !SelfCheck() {
		t.Fatal("SelfCheck() = false")
	}
}
