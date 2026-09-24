package exc

import (
	"strconv"
	"testing"
)

// TestTouchedBounded is a white-box test: after loading m distinct rows on
// both sides (m sweeps 100..10000), one more change on a single row must
// touch a number of row-state entries independent of m (<=6). The count is
// read from the unexported field directly, never via an exported method.
func TestTouchedBounded(t *testing.T) {
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		o := New(2*m + 1)
		chs := make([]Change, 0, 2*m)
		for i := 0; i < m; i++ {
			x := "r" + strconv.Itoa(i)
			chs = append(chs, Change{x, Left, 1}, Change{x, Right, 1})
		}
		if _, err := o.Apply(chs); err != nil {
			t.Fatalf("m=%d: load failed: %v", m, err)
		}
		// Change an existing row on both the left and right sides.
		for _, c := range []Change{{"r0", Left, 1}, {"r0", Right, -1}, {"r1", Left, 1}} {
			if _, err := o.Apply([]Change{c}); err != nil {
				t.Fatalf("m=%d: probe failed: %v", m, err)
			}
			if o.touched <= 0 {
				t.Fatalf("m=%d: touched counter never advanced", m)
			}
			if o.touched > 6 {
				t.Fatalf("m=%d: touched=%d grows with table size (want <=6)", m, o.touched)
			}
			if first < 0 {
				first = o.touched
			} else if o.touched != first {
				t.Fatalf("m=%d: touched=%d differs from smallest-scale value %d", m, o.touched, first)
			}
		}
	}
}

// TestOperatorRollback is a white-box check that a rejected batch restores
// multiplicities, the view, and the union counter exactly.
func TestOperatorRollback(t *testing.T) {
	cases := []struct {
		name string
		max  int
		seed []Change
		bad  []Change
		want map[string]int
	}{
		{"underflow", 10, []Change{{"a", Left, 2}, {"a", Right, 1}},
			[]Change{{"b", Left, 1}, {"a", Left, -5}}, map[string]int{"a": 1}},
		{"toomany", 1, nil, []Change{{"a", Left, 1}, {"b", Left, 1}}, map[string]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := New(tc.max)
			if _, err := o.Apply(tc.seed); err != nil {
				t.Fatal(err)
			}
			beforeUnion := o.union
			if _, err := o.Apply(tc.bad); err == nil {
				t.Fatal("expected rejection")
			}
			if got := o.View(); !eq(got, tc.want) {
				t.Fatalf("view after reject = %v, want %v", got, tc.want)
			}
			if o.union != beforeUnion {
				t.Fatalf("union=%d after rollback, want %d", o.union, beforeUnion)
			}
			if o.l.Len()+o.r.Len() == 0 && len(tc.seed) > 0 {
				t.Fatal("side multisets were not restored")
			}
		})
	}
}

// TestUnionSlotRecycled is a white-box check of the spec rule that a row
// back to zero on both sides no longer counts toward maxRows: after
// add/delete of a at maxRows=1, a distinct row b must still be accepted.
func TestUnionSlotRecycled(t *testing.T) {
	cases := []struct {
		name string
		seed []Change
	}{
		{"left-add-del", []Change{{"a", Left, 1}, {"a", Left, -1}}},
		{"both-then-clear", []Change{{"a", Left, 1}, {"a", Right, 1}, {"a", Right, -1}, {"a", Left, -1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := New(1)
			if _, err := o.Apply(tc.seed); err != nil {
				t.Fatal(err)
			}
			if o.union != 0 {
				t.Fatalf("union=%d, want 0", o.union)
			}
			if _, err := o.Apply([]Change{{"b", Right, 1}}); err != nil {
				t.Fatalf("freed slot not reused: %v", err)
			}
			if o.union != 1 {
				t.Fatalf("union=%d, want 1", o.union)
			}
		})
	}
}

func eq(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
