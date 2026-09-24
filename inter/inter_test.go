package inter

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/mset"
)

// The unexported checked counter must stay a small constant no matter how
// many distinct values the table holds: Apply locates one value by hash,
// it never scans. Table-driven over several scales.
func TestCheckedCountIndependentOfTableSize(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			tb := New()
			for i := 0; i < m; i++ {
				if _, err := tb.Apply(mset.L, fmt.Sprintf("v%d", i), 1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tb.Apply(mset.R, "v0", 1); err != nil {
				t.Fatal(err)
			}
			if tb.checked > 2 {
				t.Fatalf("checked %d values with table size %d; want a small constant", tb.checked, m)
			}
		})
	}
}

// Invariant 3: only threshold crossings emit; the 8-step trace of NOTES.md.
func TestCrossingOnly(t *testing.T) {
	want := []string{".", "+", ".", "+", ".", ".", "-", "."}
	steps := []struct {
		s mset.Side
		d int
	}{{mset.L, 1}, {mset.R, 1}, {mset.R, 1}, {mset.L, 1}, {mset.R, 1}, {mset.R, -1}, {mset.R, -1}, {mset.L, -1}}
	tb := New()
	for i, st := range steps {
		cs, err := tb.Apply(st.s, "a", st.d)
		if err != nil {
			t.Fatal(err)
		}
		got := "."
		if len(cs) == 1 && cs[0].Delta == 1 {
			got = "+"
		} else if len(cs) == 1 && cs[0].Delta == -1 {
			got = "-"
		} else if len(cs) > 1 {
			got = "?"
		}
		if got != want[i] {
			t.Fatalf("step %d: output %q, want %q", i+1, got, want[i])
		}
	}
}

// Apply must expand a multi-crossing delta into unit changelog entries and
// reject bad input without touching state.
func TestApplyTable(t *testing.T) {
	cases := []struct {
		name    string
		ops     [][3]any // side, val, d
		wantOut []int    // deltas emitted per op
		wantM   map[string]int
	}{
		{"crossing pair", [][3]any{{mset.L, "a", 1}, {mset.R, "a", 1}}, []int{0, 1}, map[string]int{"a": 1}},
		{"surplus silent", [][3]any{{mset.L, "a", 1}, {mset.R, "a", 3}}, []int{0, 1}, map[string]int{"a": 1}},
		{"multi-cross expands", [][3]any{{mset.L, "a", 2}, {mset.R, "a", 2}}, []int{0, 2}, map[string]int{"a": 2}},
		{"null never matches", [][3]any{{mset.L, mset.Null, 1}, {mset.R, mset.Null, 1}}, []int{0, 0}, map[string]int{mset.Null: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := New()
			for i, o := range tc.ops {
				cs, err := tb.Apply(o[0].(mset.Side), o[1].(string), o[2].(int))
				if err != nil {
					t.Fatal(err)
				}
				sum := 0
				for _, c := range cs {
					if c.Delta != 1 && c.Delta != -1 {
						t.Fatalf("op %d: non-unit delta %d", i, c.Delta)
					}
					sum += c.Delta
				}
				if sum != tc.wantOut[i] {
					t.Fatalf("op %d: emitted sum %d, want %d", i, sum, tc.wantOut[i])
				}
			}
			for v, want := range tc.wantM {
				if got := tb.View()[v]; got != want {
					t.Fatalf("view[%q]=%d, want %d", v, got, want)
				}
			}
		})
	}
}

// Concurrency: many goroutines read the same filled table; every view must
// be field-by-field identical. No sleeps.
func TestConcurrentViewsIdentical(t *testing.T) {
	tb := New()
	for i := 0; i < 64; i++ {
		v := fmt.Sprintf("k%d", i)
		tb.Apply(mset.L, v, 2)
		tb.Apply(mset.R, v, 1)
	}
	want := tb.View()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if !reflect.DeepEqual(want, tb.View()) {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("concurrent views differ")
	}
}
