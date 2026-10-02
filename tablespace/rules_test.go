package tablespace

import "testing"

func usedOf(t *testing.T, a *Allocator, s int) int {
	t.Helper()
	u, err := a.Used(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Pre-call used F-1 still allocates via fragment mode; when used reaches F
// the next call switches to exclusive mode.
func TestRuleThresholdBoundary(t *testing.T) {
	a, _ := New(4, 3, 6)
	s := a.NewSegment()
	// Calls decided with pre-call used 0,1,2 (<F): all fragment.
	for _, want := range []int{0, 1, 2} {
		mustAlloc(t, a, s, -1, want)
	}
	if u := usedOf(t, a, s); u != 3 {
		t.Fatalf("used=%d want 3 (== F-1 decision then ==F state)", u)
	}
	if st := a.Snapshot().Extents[0].State; st != StateFrag {
		t.Fatalf("extent0=%s want FRAG", st)
	}
	// Next call sees used==3==F: exclusive, smallest FREE is extent 1.
	mustAlloc(t, a, s, -1, 4)
	if st := a.Snapshot().Extents[1].State; st != "SEG(1)" {
		t.Fatalf("extent1=%s want SEG(1)", st)
	}
}

// Fragment allocation chooses the smallest FRAG extent, not the most
// recently used one.
func TestRuleSmallestFragSelectionPrecise(t *testing.T) {
	a, _ := New(4, 4, 6)
	s1 := a.NewSegment()
	// Fill ext0 to FULLFRAG with four segments.
	s2, s3, s4 := a.NewSegment(), a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s1, -1, 0)
	mustAlloc(t, a, s2, -1, 1)
	mustAlloc(t, a, s3, -1, 2)
	mustAlloc(t, a, s4, -1, 3)
	// New FRAG extent via smallest FREE: ext1.
	s5 := a.NewSegment()
	mustAlloc(t, a, s5, -1, 4)
	// Fill ext1 so the next fragment extent is ext2.
	s6, s7, s8 := a.NewSegment(), a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s6, -1, 5)
	mustAlloc(t, a, s7, -1, 6)
	mustAlloc(t, a, s8, -1, 7) // ext1 FULLFRAG
	s9 := a.NewSegment()
	mustAlloc(t, a, s9, -1, 8) // ext2 -> FRAG, page 8
	// Free page 1: ext0 returns to FRAG and is now the smallest.
	if err := a.FreePage(s2, 1); err != nil {
		t.Fatal(err)
	}
	s10 := a.NewSegment()
	mustAlloc(t, a, s10, -1, 1)
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// FRAG fills to FULLFRAG, frees return it to FRAG, and freeing the last
// occupied page makes it FREE.
func TestRuleFragTransitions(t *testing.T) {
	a, _ := New(3, 3, 2)
	s1, s2, s3 := a.NewSegment(), a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s1, -1, 0)
	mustAlloc(t, a, s2, -1, 1)
	mustAlloc(t, a, s3, -1, 2)
	if st := a.Snapshot().Extents[0].State; st != StateFullFrag {
		t.Fatalf("want FULLFRAG, got %s", st)
	}
	if err := a.FreePage(s2, 1); err != nil {
		t.Fatal(err)
	}
	if st := a.Snapshot().Extents[0].State; st != StateFrag {
		t.Fatalf("want FRAG, got %s", st)
	}
	if err := a.FreePage(s1, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.FreePage(s3, 2); err != nil {
		t.Fatal(err)
	}
	if st := a.Snapshot().Extents[0].State; st != StateFree {
		t.Fatalf("want FREE, got %s", st)
	}
	s4 := a.NewSegment()
	mustAlloc(t, a, s4, -1, 0) // reuse ext0 minimum page
}
