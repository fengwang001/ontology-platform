package cwagg

import (
	"strconv"
	"testing"
)

// TestCheckedWindowsConstant proves the trigger test locates the target
// window directly via floor(Pos/size): the number of windows inspected on
// the triggering Add is 1 no matter how many open windows exist.
func TestCheckedWindowsConstant(t *testing.T) {
	const size = 4
	for _, m := range []int64{100, 1000, 10000} {
		t.Run(strconv.FormatInt(m, 10), func(t *testing.T) {
			a := New(size, 0)
			for i := int64(0); i < m; i++ { // m open windows, each with size-1 elements
				key := strconv.FormatInt(i, 10)
				for p := int64(0); p < size-1; p++ {
					a.Add(Event{Key: key, Pos: p})
				}
			}
			if n := len(a.Fired()); n != 0 {
				t.Fatalf("m=%d: %d windows fired early", m, n)
			}
			a.Add(Event{Key: "0", Pos: size - 1}) // fires exactly one window
			if a.checked != 1 {
				t.Fatalf("m=%d: checked=%d windows, want 1 (O(1) lookup)", m, a.checked)
			}
			if n := len(a.Fired()); n != 1 {
				t.Fatalf("m=%d: fired=%d, want 1", m, n)
			}
		})
	}
}
