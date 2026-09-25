package mbatch

import (
	"reflect"
	"strconv"
	"testing"

	"ontology/win"
)

func chg(k, v int64, plus bool) Change {
	return Change{Key: "K", Win: win.Of(k*10, 10), Value: v, Plus: plus}
}

func evs(ts ...int64) []Event {
	out := make([]Event, len(ts))
	for i, t := range ts {
		out[i] = Event{Key: "K", TS: t}
	}
	return out
}

// TestBatchEmissions pins the four rows of NOTES.md: three triggers + Flush.
func TestBatchEmissions(t *testing.T) {
	m, err := New(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]Change{
		{chg(0, 1, true), chg(1, 1, true)},
		{chg(0, 1, false), chg(0, 2, true)},
		{chg(2, 3, true), chg(0, 2, false), chg(0, 3, true), chg(1, 1, false), chg(1, 2, true)},
		{chg(3, 1, true), chg(0, 3, false), chg(0, 4, true)},
	}
	for i, ts := range [][]int64{{5, 12, 20}, {7, 22, 25}, {8, 31, 15}} {
		got, err := m.Feed(evs(ts...))
		if err != nil || !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("batch %d got=%v err=%v want=%v", i, got, err, want[i])
		}
	}
	if _, err := m.Feed(evs(9)); err != nil { // tenth event waits in the tail batch
		t.Fatal(err)
	}
	if got := m.Flush(); !reflect.DeepEqual(got, want[3]) {
		t.Fatalf("flush got=%v want=%v", got, want[3])
	}
	final := map[Cell]int64{{"K", 0}: 4, {"K", 1}: 2, {"K", 2}: 3, {"K", 3}: 1}
	if got := m.View(); !reflect.DeepEqual(got, final) {
		t.Fatalf("view=%v want=%v", got, final)
	}
}

// TestCloseProbeBound proves closures are located by ordered end time, not a
// table scan: closing k=3 windows probes exactly k+1 and closing none peeks
// once, both independent of the number m of accumulated open windows.
func TestCloseProbeBound(t *testing.T) {
	cases := []struct{ m int }{{100}, {1000}, {10000}}
	var seen []int64
	for _, tc := range cases {
		c, _ := New(10, 4)
		for i := 0; i < tc.m; i++ { // m distinct open cells, all at end 910
			if _, err := c.Feed([]Event{{"k" + strconv.Itoa(i), 900}}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.Feed([]Event{{"L0", 0}, {"L1", 10}, {"L2", 20}, {"k0", 902}}); err != nil {
			t.Fatal(err)
		}
		if c.closeProbes != 4 { // k=3 closures + one peek of end 910 > wm 902
			t.Fatalf("m=%d closing probes=%d, want 4 (k+1)", tc.m, c.closeProbes)
		}
		seen = append(seen, c.closeProbes)
		if _, err := c.Feed([]Event{{"k0", 903}, {"k1", 903}, {"k2", 903}, {"k3", 903}}); err != nil {
			t.Fatal(err)
		}
		if c.closeProbes != 1 { // zero closures: a single minimum-end peek
			t.Fatalf("m=%d zero-close probes=%d, want 1", tc.m, c.closeProbes)
		}
	}
	for _, p := range seen { // constant across two orders of magnitude
		if p != seen[0] {
			t.Fatalf("probe count grew with m: %v", seen)
		}
	}
}
