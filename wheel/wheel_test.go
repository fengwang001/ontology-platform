package wheel

import (
	"testing"

	"ontology/slot"
)

func TestAdvanceWrap(t *testing.T) {
	cases := []struct {
		size  int
		steps int
		pos   int
		wraps int
	}{
		{4, 1, 1, 0},
		{4, 4, 0, 1},
		{4, 9, 1, 2},
		{64, 63, 63, 0},
		{64, 64, 0, 1},
	}
	for _, c := range cases {
		w := New(c.size)
		wraps := 0
		for i := 0; i < c.steps; i++ {
			if w.Advance() {
				wraps++
			}
		}
		if w.Pos() != c.pos || wraps != c.wraps {
			t.Fatalf("size=%d steps=%d: pos=%d wraps=%d, want %d/%d",
				c.size, c.steps, w.Pos(), wraps, c.pos, c.wraps)
		}
	}
}

func TestOverflow(t *testing.T) {
	cases := []struct {
		units int64
		want  bool
	}{
		{0, false}, {63, false}, {64, true}, {100, true},
	}
	w := New(64)
	for _, c := range cases {
		if got := w.Overflow(c.units); got != c.want {
			t.Fatalf("Overflow(%d) = %v, want %v", c.units, got, c.want)
		}
	}
}

func TestLenAndSlots(t *testing.T) {
	w := New(8)
	w.Slot(3).Insert(slot.Entry{H: 1, Seq: 1})
	w.Slot(3).Insert(slot.Entry{H: 2, Seq: 2})
	w.Current().Insert(slot.Entry{H: 3, Seq: 3})
	if w.Len() != 3 {
		t.Fatalf("Len = %d, want 3", w.Len())
	}
	if w.Current() != w.Slot(0) {
		t.Fatal("Current should be slot 0 initially")
	}
}
