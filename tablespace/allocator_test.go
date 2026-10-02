package tablespace

import (
	"errors"
	"testing"
)

func mustAlloc(t *testing.T, a *Allocator, s, hint, want int) {
	t.Helper()
	got, err := a.AllocPage(s, hint)
	if err != nil {
		t.Fatalf("AllocPage(%d,%d) unexpected error %v", s, hint, err)
	}
	if got != want {
		t.Fatalf("AllocPage(%d,%d) = %d, want %d", s, hint, got, want)
	}
}

func TestSpecExampleOne(t *testing.T) {
	a, err := New(8, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	s1 := a.NewSegment() // 1
	mustAlloc(t, a, s1, -1, 0)
	mustAlloc(t, a, s1, -1, 1)
	mustAlloc(t, a, s1, -1, 2)
	mustAlloc(t, a, s1, -1, 3)
	mustAlloc(t, a, s1, -1, 8) // used==4 -> exclusive, extent 1
	mustAlloc(t, a, s1, 12, 12)

	s2 := a.NewSegment()       // 2
	mustAlloc(t, a, s2, -1, 4) // smallest FRAG extent 0, min free page

	snap := a.Snapshot()
	if snap.Extents[0].State != StateFrag || snap.Extents[1].State != "SEG(1)" {
		t.Fatalf("unexpected extent states: %v", snap.Extents)
	}
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestSpecExampleTwo(t *testing.T) {
	a, _ := New(8, 4, 2)
	s1, s2 := a.NewSegment(), a.NewSegment()
	for i := 0; i < 4; i++ {
		mustAlloc(t, a, s1, -1, i)
	}
	for i := 0; i < 4; i++ {
		mustAlloc(t, a, s2, -1, 4+i)
	}
	mustAlloc(t, a, s1, -1, 8)

	s3 := a.NewSegment()
	_, err := a.AllocPage(s3, -1)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("want ErrNoSpace, got %v", err)
	}
	snap := a.Snapshot()
	if snap.Extents[0].State != StateFullFrag || snap.Extents[1].State != "SEG(1)" {
		t.Fatalf("unexpected states after no-space: %v", snap.Extents)
	}
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidConstruction(t *testing.T) {
	for _, args := range [][3]int{
		{1, 1, 1}, {1025, 1, 1}, {8, 0, 1}, {8, 9, 1}, {8, 4, 0}, {8, 4, 100001},
	} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) err=%v, want ErrInvalidArgument", args, err)
		}
	}
}
