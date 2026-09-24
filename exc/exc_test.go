package exc

import (
	"fmt"
	"testing"
)

// TestTouchBound: with m distinct rows live on both sides, one more change
// to a single row must touch only a constant number of row-state entries —
// never proportional to m (no full-table recompute or scan).
func TestTouchBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			op := New(m + 1)
			for i := 0; i < m; i++ {
				row := fmt.Sprintf("row%d", i)
				if _, _, err := op.Apply(Change{Side: L, Row: row, Delta: 1}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := op.Apply(Change{Side: R, Row: row, Delta: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := op.Apply(Change{Side: L, Row: "row0", Delta: 1}); err != nil {
				t.Fatal(err)
			}
			if op.touched > maxTouches {
				t.Fatalf("m=%d: touched %d entries, want <= %d", m, op.touched, maxTouches)
			}
		})
	}
}

// TestSentinelErrors: the three rejection classes map to three distinct
// sentinel errors.
func TestSentinelErrors(t *testing.T) {
	op := New(1)
	if _, _, err := op.Apply(Change{Side: L, Row: "a", Delta: 1}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		c    Change
		want error
	}{
		{"empty row", Change{Side: L, Row: "", Delta: 1}, ErrInvalidChange},
		{"zero delta", Change{Side: L, Row: "a", Delta: 0}, ErrInvalidChange},
		{"bad side", Change{Side: Side(7), Row: "a", Delta: 1}, ErrInvalidChange},
		{"underflow", Change{Side: R, Row: "a", Delta: -1}, ErrUnderflow},
		{"too many rows", Change{Side: L, Row: "b", Delta: 1}, ErrTooManyRows},
	}
	for _, tc := range cases {
		if _, _, err := op.Apply(tc.c); err != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if ErrInvalidChange == ErrUnderflow || ErrUnderflow == ErrTooManyRows || ErrInvalidChange == ErrTooManyRows {
		t.Fatal("sentinels must be mutually distinct")
	}
}

// tenStep is the NOTES.md section-3 sequence; it emits 7 changelog entries.
var tenStep = []Change{
	{Side: R, Row: "a", Delta: 1}, {Side: L, Row: "a", Delta: 1},
	{Side: L, Row: "a", Delta: 1}, {Side: L, Row: "a", Delta: 2},
	{Side: R, Row: "a", Delta: 1}, {Side: R, Row: "a", Delta: 3},
	{Side: L, Row: "a", Delta: -1}, {Side: R, Row: "a", Delta: -4},
	{Side: L, Row: "b", Delta: 1}, {Side: R, Row: "b", Delta: 1},
}

// TestViewNonNegative: after every change the view holds only positive
// multiplicities — no zero rows, never negative.
func TestViewNonNegative(t *testing.T) {
	seqs := map[string][]Change{
		"ten-step": tenStep,
		"drain-to-zero": {
			{Side: L, Row: "x", Delta: 3}, {Side: R, Row: "x", Delta: 3},
			{Side: L, Row: "x", Delta: 1}, {Side: R, Row: "x", Delta: 1},
		},
	}
	for name, seq := range seqs {
		t.Run(name, func(t *testing.T) {
			op := New(10)
			for _, c := range seq {
				if _, _, err := op.Apply(c); err != nil {
					t.Fatal(err)
				}
				for row, n := range op.View() {
					if n <= 0 {
						t.Fatalf("after %+v: view[%q] = %d", c, row, n)
					}
				}
			}
		})
	}
}

// TestMinimalChange: each input change yields at most one output, with
// non-zero delta no larger in magnitude than the input delta.
func TestMinimalChange(t *testing.T) {
	op := New(10)
	emits := 0
	for _, c := range tenStep {
		out, emit, err := op.Apply(c)
		if err != nil {
			t.Fatal(err)
		}
		if !emit {
			continue
		}
		emits++
		d := out.Delta
		if out.Row != c.Row || d == 0 || max(d, -d) > max(c.Delta, -c.Delta) {
			t.Fatalf("output %+v not minimal for %+v", out, c)
		}
	}
	if emits != 7 {
		t.Fatalf("ten-step sequence emitted %d entries, want 7", emits)
	}
}

// TestRejectedNoTrace: a rejected change mutates nothing; operator still works.
func TestRejectedNoTrace(t *testing.T) {
	op := New(2)
	if _, _, err := op.Apply(Change{Side: L, Row: "a", Delta: 2}); err != nil {
		t.Fatal(err)
	}
	before := op.View()
	for _, c := range []Change{
		{Side: L, Row: "", Delta: 1},
		{Side: L, Row: "a", Delta: 0},
		{Side: R, Row: "a", Delta: -5},
		{Side: L, Row: "b", Delta: 1}, // accepted: cap is 2, only a is live
	} {
		_, _, _ = op.Apply(c)
	}
	if _, _, err := op.Apply(Change{Side: L, Row: "c", Delta: 1}); err != ErrTooManyRows {
		t.Fatalf("cap: got %v", err)
	}
	view := op.View()
	if len(view) != len(before)+1 || view["a"] != 2 || view["b"] != 1 {
		t.Fatalf("state drifted: %v (before %v)", view, before)
	}
	if _, _, err := op.Apply(Change{Side: R, Row: "a", Delta: 1}); err != nil {
		t.Fatalf("operator unusable after rejections: %v", err)
	}
	if got := op.View()["a"]; got != 1 {
		t.Fatalf("view[a] = %d, want 1", got)
	}
}
