package sch

import "testing"

func TestDefResolution(t *testing.T) {
	c := NewStandard()
	cases := []struct {
		f    string
		V    int
		want int
	}{
		{"a", 1, 1}, {"a", 2, 1}, {"a", 3, 1},
		{"b", 1, 2}, {"b", 2, 2}, {"b", 3, 2}, // removed after v2 -> frozen at 2
		{"c", 1, 10}, {"c", 2, 10}, {"c", 3, 30}, // intro default 10 until redefined
		{"d", 1, 20}, {"d", 2, 20}, {"d", 3, 20}, // not introduced until v3 -> 20
	}
	for _, tc := range cases {
		if got := c.Def(tc.f, tc.V); got != tc.want {
			t.Errorf("Def(%q,%d)=%d want %d", tc.f, tc.V, got, tc.want)
		}
	}
}

func TestFieldsAndHas(t *testing.T) {
	c := NewStandard()
	wantFields := [][]string{{"a", "b"}, {"a", "b", "c"}, {"a", "c", "d"}}
	for V := 1; V <= 3; V++ {
		got := c.Fields(V)
		if len(got) != len(wantFields[V-1]) {
			t.Fatalf("v%d fields %v", V, got)
		}
		for i, f := range wantFields[V-1] {
			if got[i] != f || !c.Has(V, f) {
				t.Fatalf("v%d field mismatch: %v", V, got)
			}
		}
	}
	if c.Has(1, "c") || c.Has(3, "b") || c.Has(0, "a") || c.Has(4, "a") {
		t.Fatal("Has reported wrong membership")
	}
}

// TestProbeCountConstant proves missing-default resolution is a direct index:
// a field introduced only at version m is resolved from writer version 1 and
// the inspected-entry count must stay a small constant regardless of m.
func TestProbeCountConstant(t *testing.T) {
	ms := []int{100, 1000, 10000}
	const bound = 3 // small constant, never proportional to m
	for _, m := range ms {
		hist := make([]Version, m)
		for v := range hist {
			hist[v].Fields = []Field{{Name: "x", Def: 1}}
		}
		hist[m-1].Fields = append(hist[m-1].Fields, Field{Name: "late", Def: 7})
		c := New(hist)
		if got := c.Def("late", 1); got != 7 { // missing at writer v1 -> intro default
			t.Fatalf("m=%d late default=%d want 7", m, got)
		}
		if p := c.probeCount.Load(); p > bound {
			t.Fatalf("m=%d probeCount=%d > %d (linear scan?)", m, p, bound)
		}
		if got := c.Def("x", m); got != 1 || c.probeCount.Load() > bound {
			t.Fatalf("m=%d x probe: val=%d count=%d", m, got, c.probeCount.Load())
		}
	}
}
