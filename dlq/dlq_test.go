package dlq

import (
	"math/bits"
	"testing"
)

func TestPopDueOrder(t *testing.T) {
	type op struct {
		id     string
		fire   int64
		cancel bool
	}
	cases := []struct {
		name string
		ops  []op
		tick []int64 // successive PopDue times; want aligns 1:1
		want [][]string
	}{
		{"by fireAt", []op{{"z", 5, false}, {"a", 1, false}, {"m", 3, false}},
			[]int64{10}, [][]string{{"a", "m", "z"}}},
		{"tie uses registration seq", []op{{"a", 5, false}, {"c", 5, false}, {"b", 5, false}},
			[]int64{5}, [][]string{{"a", "c", "b"}}},
		{"section-3 eight steps", []op{{"a", 5, false}, {"b", 3, true}, {"c", 5, false},
			{"d", 2, false}, {"b", 4, false}}, []int64{5}, [][]string{{"d", "b", "a", "c"}}},
		{"left-closed boundary", []op{{"a", 5, false}},
			[]int64{4, 5}, [][]string{{}, {"a"}}},
		{"negative fireAt", []op{{"n", -3, false}}, []int64{0}, [][]string{{"n"}}},
		{"cancelled generation skipped", []op{{"b", 3, true}, {"b", 4, false}, {"x", 9, false}},
			[]int64{5, 9}, [][]string{{"b"}, {"x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			handles := map[string]*Handle{}
			for _, o := range tc.ops {
				hd := h.Push(o.id, o.fire)
				if o.cancel {
					handles[o.id] = hd
				}
			}
			for _, o := range tc.ops {
				if o.cancel {
					handles[o.id].Cancel()
				}
			}
			for i, now := range tc.tick {
				got := h.PopDue(now)
				if !eq(got, tc.want[i]) {
					t.Fatalf("tick %d: got %v want %v", now, got, tc.want[i])
				}
			}
		})
	}
}

func eq(a, b []string) bool {
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

// TestPopInspectCounter proves one due pop amid m far-future entries touches
// only O(log m) nodes rather than scanning the table. The unexported counter
// is read directly here, never through an exported function or method.
func TestPopInspectCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		for i := 0; i < m; i++ {
			h.Push("f", int64(1000+i))
		}
		h.Push("early", 1)
		before := h.inspected
		got := h.PopDue(1)
		if !eq(got, []string{"early"}) {
			t.Fatalf("m=%d: got %v", m, got)
		}
		bound := 4 * bits.Len(uint(m))
		if h.inspected > bound {
			t.Fatalf("m=%d: inspected %d exceeds O(log m) bound %d", m, h.inspected, bound)
		}
		if h.inspected >= m/2 {
			t.Fatalf("m=%d: inspected %d grows linearly with m", m, h.inspected)
		}
		if before != 0 {
			t.Fatalf("counter not reset at PopDue entry: %d", before)
		}
	}
}
