package slot

import (
	"math/bits"
	"testing"
)

// TestOpenCheckedSlotsLog pins the logarithmic bound: every Open must
// inspect at most ceil(log2 C)+1 slots, across heavy slot reuse.
func TestOpenCheckedSlotsLog(t *testing.T) {
	for _, c := range []int{100, 257, 1000, 4096, 10000} {
		tab := New(c)
		bound := bits.Len(uint(c-1)) + 1
		var live []Handle
		for round := 0; round < 20; round++ {
			for i := 0; i < c/4; i++ { // open a quarter of the slots
				h, err := tab.Open()
				if err != nil {
					t.Fatalf("C=%d round %d: %v", c, round, err)
				}
				if tab.checked > bound {
					t.Fatalf("C=%d: Open inspected %d slots, bound %d", c, tab.checked, bound)
				}
				live = append(live, h)
			}
			for i := 0; i < len(live); i += 3 { // close every third, forcing reuse
				tab.Free(live[i].ID)
			}
			kept := live[:0]
			for i, h := range live {
				if i%3 != 0 {
					kept = append(kept, h)
				}
			}
			live = kept
		}
		if !tab.BoundOK() {
			t.Fatalf("C=%d: BoundOK false", c)
		}
	}
}

// TestMinFreeAndGeneration pins min-free reuse and generation semantics.
func TestMinFreeAndGeneration(t *testing.T) {
	tab := New(4)
	var hs []Handle
	for i := 0; i < 4; i++ {
		h, err := tab.Open()
		if err != nil || h != (Handle{ID: i, Gen: 1}) {
			t.Fatalf("open %d: %v %v", i, h, err)
		}
		hs = append(hs, h)
	}
	if _, err := tab.Open(); err != ErrNoSlots {
		t.Fatalf("want ErrNoSlots, got %v", err)
	}
	tab.Free(2)
	tab.Free(1) // free 1 and 2; min-free must pick 1
	if h, _ := tab.Open(); h != (Handle{ID: 1, Gen: 2}) {
		t.Fatalf("reuse: %v", h)
	}
	if open, gen, _ := tab.State(2); open || gen != 1 {
		t.Fatalf("gen must survive close: %v %v", open, gen)
	}
	if _, _, err := tab.State(4); err != ErrBadID {
		t.Fatalf("want ErrBadID, got %v", err)
	}
}
