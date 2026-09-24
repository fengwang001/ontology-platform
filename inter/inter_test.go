package inter

import (
	"fmt"
	"testing"

	"ontology/mset"
)

// TestProbeCountConstant proves values are located by hash lookup, not by
// scanning all values: after seeding m distinct left copies, applying one
// right copy to a known value must inspect only a small constant number of
// value entries regardless of m.
func TestProbeCountConstant(t *testing.T) {
	const bound = 4 // independent of m
	tiers := []int{100, 1000, 10000}
	for _, m := range tiers {
		v := New()
		for i := 0; i < m; i++ {
			if _, err := v.Apply(mset.L, fmt.Sprintf("v%05d", i), 1); err != nil {
				t.Fatalf("m=%d seed i=%d: %v", m, i, err)
			}
		}
		target := fmt.Sprintf("v%05d", m/2)
		updates := []struct {
			side mset.Side
			d    int
		}{{mset.R, 1}, {mset.R, 1}, {mset.L, 1}}
		for _, u := range updates {
			if _, err := v.Apply(u.side, target, u.d); err != nil {
				t.Fatalf("m=%d apply: %v", m, err)
			}
			if v.probed > bound {
				t.Fatalf("m=%d probed %d entries, want <= %d (must not grow linearly with m)",
					m, v.probed, bound)
			}
		}
	}
}

// TestRejectLeavesNoEntry checks that a rejected Apply neither inserts a new
// map entry nor mutates an existing one (failure leaves no trace at inter).
func TestRejectLeavesNoEntry(t *testing.T) {
	cases := []struct {
		name string
		side mset.Side
		val  string
		d    int
		err  error
	}{
		{"zero delta", mset.L, "a", 0, mset.ErrZeroDelta},
		{"negative on absent", mset.L, "b", -1, mset.ErrCountNegative},
		{"negative on present", mset.R, "a", -1, mset.ErrCountNegative},
		{"empty value", mset.L, "", 1, ErrEmptyVal},
		{"bad side", mset.Side(9), "a", 1, mset.ErrBadSide},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := New()
			if _, err := v.Apply(mset.L, "a", 2); err != nil {
				t.Fatal(err)
			}
			before := len(v.cells)
			if _, err := v.Apply(tc.side, tc.val, tc.d); err != tc.err {
				t.Fatalf("want %v, got %v", tc.err, err)
			}
			if len(v.cells) != before {
				t.Fatalf("entry count changed %d -> %d", before, len(v.cells))
			}
			if l, r := v.cells["a"].Counts(); l != 2 || r != 0 {
				t.Fatalf("existing counts mutated: %d,%d", l, r)
			}
		})
	}
}
