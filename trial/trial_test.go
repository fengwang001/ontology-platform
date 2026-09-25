package trial

import (
	"errors"
	"testing"

	"ontology/refc"
)

// TestPointChecksBounded proves Point examines only a constant number of
// object records regardless of how many live objects exist. It reads the
// unexported counter directly (white box); no exported API exposes it.
func TestPointChecksBounded(t *testing.T) {
	const bound = 3 // from + to (+nothing else); must never scale with heap size
	for _, m := range []int{100, 1000, 10000} {
		h := refc.New()
		c := NewCollector(h)
		h.Lock()
		ids := make([]refc.Obj, m)
		for i := range ids {
			ids[i] = h.Root().ID
		}
		// Successful Point to a live target checks exactly from and to.
		if err := c.Point(ids[0], ids[1]); err != nil {
			t.Fatalf("m=%d Point: %v", m, err)
		}
		if c.lastPointChecks > bound {
			t.Fatalf("m=%d Point examined %d records, want <= %d", m, c.lastPointChecks, bound)
		}
		if c.lastPointChecks != 2 {
			t.Fatalf("m=%d checks=%d, want 2 (from,to)", m, c.lastPointChecks)
		}
		// Only ids[1] gained an rc; a far bystander is untouched (locality).
		if b, _ := h.Get(ids[1]); b.RC != 2 {
			t.Fatalf("m=%d target rc=%d want 2", m, b.RC)
		}
		if z, _ := h.Get(ids[m-1]); z.RC != 1 {
			t.Fatalf("m=%d bystander rc=%d want 1", m, z.RC)
		}
		// Point to nil checks only the from record.
		if err := c.Point(ids[2], 0); err != nil {
			t.Fatalf("m=%d Point nil: %v", m, err)
		}
		if c.lastPointChecks != 1 {
			t.Fatalf("m=%d nil-point checks=%d want 1", m, c.lastPointChecks)
		}
		// A rejected Point still records a bounded, local check count.
		if err := c.Point(ids[3], refc.Obj(1<<40)); !errors.Is(err, ErrInvalidObject) {
			t.Fatalf("m=%d want ErrInvalidObject, got %v", m, err)
		}
		if c.lastPointChecks > bound {
			t.Fatalf("m=%d rejected Point examined %d, want <= %d", m, c.lastPointChecks, bound)
		}
		h.Unlock()
	}
}

// TestCollectCyclesWhiteBox directly drives Collect over a cycle, a rooted
// cycle and a self-loop, checking the exact sweep counts and survivors.
func TestCollectCyclesWhiteBox(t *testing.T) {
	cases := []struct {
		name       string
		want, left int
		build      func(h *refc.Heap)
	}{
		{"unrooted cycle", 2, 0, func(h *refc.Heap) {
			a, b := h.Root(), h.Root()
			h.Repoint(a, b.ID)
			h.Repoint(b, a.ID)
			h.DropRoot(a.ID)
			h.DropRoot(b.ID)
		}},
		{"rooted cycle rescued", 0, 2, func(h *refc.Heap) {
			a, b := h.Root(), h.Root()
			h.Repoint(a, b.ID)
			h.Repoint(b, a.ID)
			h.DropRoot(b.ID) // a stays rooted; b reachable via a
		}},
		{"unrooted self-loop", 1, 0, func(h *refc.Heap) {
			s := h.Root()
			h.Repoint(s, s.ID)
			h.DropRoot(s.ID)
		}},
	}
	for _, tc := range cases {
		h := refc.New()
		c := NewCollector(h)
		h.Lock()
		tc.build(h)
		if got := c.Collect(); got != tc.want {
			t.Fatalf("%s: Collect freed %d want %d", tc.name, got, tc.want)
		}
		if h.Len() != tc.left {
			t.Fatalf("%s: %d records remain, want %d", tc.name, h.Len(), tc.left)
		}
		h.Unlock()
	}
}
