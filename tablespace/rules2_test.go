package tablespace

import (
	"errors"
	"sync"
	"testing"
)

// Hint is ignored when landing in another segment's extent, an occupied
// page, or a non-exclusive (FRAG/FREE) extent; fresh-extent allocation
// ignores hint as well.
func TestRuleHintIgnoredCases(t *testing.T) {
	a, _ := New(4, 2, 5)
	s1, s2 := a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s1, -1, 0) // frag page 0
	mustAlloc(t, a, s1, -1, 1) // frag page 1; used now 2
	mustAlloc(t, a, s1, 12, 4) // fresh FREE ext1; hint in FREE ext3 ignored

	mustAlloc(t, a, s2, -1, 2) // s2 frag page 2
	mustAlloc(t, a, s2, -1, 3) // s2 frag page 3; used now 2
	mustAlloc(t, a, s2, -1, 8) // s2 exclusive ext2 page 8

	// Hint inside another segment's exclusive extent -> ignored.
	mustAlloc(t, a, s1, 9, 5) // queue head ext1 -> page 5
	// Hint at an occupied page inside own extent -> ignored.
	mustAlloc(t, a, s1, 4, 6) // page 6
	// Hint inside FRAG extent 0 -> ignored.
	mustAlloc(t, a, s1, 0, 7) // page 7, ext1 full
	// Queue empty; hint inside FREE ext3 is ignored -> take ext3 anyway.
	mustAlloc(t, a, s1, 12, 12)
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// Freeing a page in a full exclusive extent queues it at the tail, behind
// extents already in the non-full queue.
func TestRuleFullExtentRequeuedAtTail(t *testing.T) {
	a, _ := New(3, 2, 6)
	s := a.NewSegment()
	// used 0,1 (<F=2): fragment pages 0,1 in ext0.
	mustAlloc(t, a, s, -1, 0)
	mustAlloc(t, a, s, -1, 1)
	// Exclusive from now on: fresh FREE ext1.
	mustAlloc(t, a, s, -1, 3) // ext1 (ext0 is FRAG with page 2 free)
	mustAlloc(t, a, s, -1, 4)
	mustAlloc(t, a, s, -1, 5) // ext1 full
	mustAlloc(t, a, s, -1, 6) // ext2 queued
	// Free a page in the older, full ext1 -> enqueued behind ext2.
	if err := a.FreePage(s, 3); err != nil {
		t.Fatal(err)
	}
	q := a.Snapshot().NonFullQueues[s]
	if len(q) != 2 || q[0] != 2 || q[1] != 1 {
		t.Fatalf("queue=%v, want [2 1]", q)
	}
	mustAlloc(t, a, s, -1, 7) // head ext2 -> page 7; ext2 full
	mustAlloc(t, a, s, -1, 3) // ext1 now head, min free page 3
	mustAlloc(t, a, s, -1, 4) // remaining slot in ext1
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// An exclusive extent whose pages all return to free goes back to FREE and
// leaves the segment's queue.
func TestRuleExclusiveReturnedFree(t *testing.T) {
	a, _ := New(3, 1, 3)
	s := a.NewSegment()
	// F=1: first call is fragment (used 0<1), so force exclusive shape
	// with F=1 via: page0 frag, then exclusive ext1.
	mustAlloc(t, a, s, -1, 0) // fragment ext0 page0
	mustAlloc(t, a, s, -1, 3) // exclusive ext1 page3
	mustAlloc(t, a, s, -1, 4)
	mustAlloc(t, a, s, -1, 5) // ext1 full
	for _, p := range []int{5, 3, 4} {
		if err := a.FreePage(s, p); err != nil {
			t.Fatal(err)
		}
	}
	snap := a.Snapshot()
	if snap.Extents[1].State != StateFree {
		t.Fatalf("ext1=%s, want FREE", snap.Extents[1].State)
	}
	if len(snap.NonFullQueues[s]) != 0 {
		t.Fatalf("queue not empty: %v", snap.NonFullQueues[s])
	}
	s2 := a.NewSegment()
	// Free the fragment page too so ext0 becomes the smallest FREE.
	if err := a.FreePage(s, 0); err != nil {
		t.Fatal(err)
	}
	mustAlloc(t, a, s2, -1, 0) // s2 first call: fragment, ext0 -> FRAG
	if st := a.Snapshot().Extents[0].State; st != StateFrag {
		t.Fatalf("ext0=%s, want FRAG", st)
	}
}

// Once frees push used back below F, allocations return to fragment mode.
func TestRuleFallbackToFragment(t *testing.T) {
	a, _ := New(4, 2, 5)
	s := a.NewSegment()
	mustAlloc(t, a, s, -1, 0)
	mustAlloc(t, a, s, -1, 1)
	mustAlloc(t, a, s, -1, 4) // exclusive ext1 page4
	if err := a.FreePage(s, 4); err != nil {
		t.Fatal(err)
	} // ext1 -> FREE, used 2 == F still exclusive
	if err := a.FreePage(s, 1); err != nil {
		t.Fatal(err)
	} // used 1 < F, back to fragment mode
	mustAlloc(t, a, s, -1, 1) // smallest FRAG ext0, min free page 1
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// Exclusive mode never borrows fragment pages, and fragment mode never uses
// free pages inside SEG extents.
func TestRuleNoCrossBorrow(t *testing.T) {
	// Spec example two: fragment alloc rejected despite free SEG pages.
	a, _ := New(8, 4, 2)
	t1, t2 := a.NewSegment(), a.NewSegment()
	for i := 0; i < 4; i++ {
		mustAlloc(t, a, t1, -1, i)
	}
	for i := 0; i < 4; i++ {
		mustAlloc(t, a, t2, -1, 4+i)
	}
	mustAlloc(t, a, t1, -1, 8) // ext1 SEG(t1)
	t3 := a.NewSegment()
	if _, err := a.AllocPage(t3, -1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("fragment alloc must not use SEG pages: %v", err)
	}

	// Exclusive mode with FRAG space but no FREE and no queued extent:
	// v1 fills its exclusive ext1; ext0 stays FRAG with a free page but
	// must never be borrowed in exclusive mode.
	c, _ := New(4, 2, 2)
	v1, v2 := c.NewSegment(), c.NewSegment()
	mustAlloc(t, c, v1, -1, 0)
	mustAlloc(t, c, v1, -1, 1)
	mustAlloc(t, c, v1, -1, 4) // ext1 SEG
	mustAlloc(t, c, v1, -1, 5)
	mustAlloc(t, c, v1, -1, 6)
	mustAlloc(t, c, v1, -1, 7) // ext1 full
	mustAlloc(t, c, v2, -1, 2) // ext0 FRAG, page 3 free
	// v1 queue empty, no FREE; FRAG ext0 has page 3 free but must not be
	// borrowed in exclusive mode.
	if _, err := c.AllocPage(v1, -1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("exclusive alloc must not borrow FRAG: %v", err)
	}
	// u1 queue empty and no FREE -> ErrNoSpace (cannot borrow FRAG ext0).
}

// FreePage rejects other-owner and free pages; error ordering: invalid args
// first, then unknown segment, then ownership.
func TestRuleFreeRejections(t *testing.T) {
	a, _ := New(4, 2, 4)
	s1, s2 := a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s1, -1, 0)

	if err := a.FreePage(s2, 0); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("other owner: %v", err)
	}
	if err := a.FreePage(s1, 1); !errors.Is(err, ErrPageNotOwned) {
		t.Fatalf("free page: %v", err)
	}
	if err := a.FreePage(s1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad page: %v", err)
	}
	if err := a.FreePage(99, 0); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("unknown segment: %v", err)
	}
	if err := a.FreePage(99, 999); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid must precede noseg: %v", err)
	}
	// Failed frees change nothing.
	snap := a.Snapshot()
	if snap.Pages[0] != s1 || snap.Pages[1] != 0 {
		t.Fatalf("state changed by rejected free: %v", snap.Pages[:4])
	}
}

// AllocPage argument/segment error ordering and no-space state preservation.
func TestRuleAllocRejections(t *testing.T) {
	a, _ := New(2, 2, 1)
	if _, err := a.AllocPage(1, -2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("hint<-1: %v", err)
	}
	if _, err := a.AllocPage(1, 2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("hint oob: %v", err)
	}
	if _, err := a.AllocPage(5, -1); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("unknown segment: %v", err)
	}
	s := a.NewSegment()
	mustAlloc(t, a, s, -1, 0)
	mustAlloc(t, a, s, -1, 1)
	before := a.Snapshot()
	if _, err := a.AllocPage(s, -1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("nospace: %v", err)
	}
	after := a.Snapshot()
	if diff := statesEqualSnap(before, after); diff != "" {
		t.Fatalf("no-space changed state: %s", diff)
	}
}

func statesEqualSnap(x, y Snapshot) string {
	for i := range x.Extents {
		if x.Extents[i] != y.Extents[i] {
			return "extent differs"
		}
	}
	for i := range x.Pages {
		if x.Pages[i] != y.Pages[i] {
			return "pages differ"
		}
	}
	return ""
}

// FreeSegment invalidates the id, and later NewSegment never reuses it.
func TestRuleFreeSegmentInvalidation(t *testing.T) {
	a, _ := New(4, 2, 4)
	s1, s2 := a.NewSegment(), a.NewSegment()
	mustAlloc(t, a, s1, -1, 0)
	mustAlloc(t, a, s2, -1, 1)
	mustAlloc(t, a, s1, -1, 2)
	if err := a.FreeSegment(s1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Used(s1); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("used after free: %v", err)
	}
	if _, err := a.AllocPage(s1, -1); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("alloc after free: %v", err)
	}
	if err := a.FreePage(s1, 0); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("free after free: %v", err)
	}
	if err := a.FreeSegment(s1); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("double freeseg: %v", err)
	}
	s3 := a.NewSegment()
	if s3 != 3 {
		t.Fatalf("id reused: new id=%d, want 3", s3)
	}
	// s2's fragment page survived s1's FreeSegment.
	if p := a.Snapshot().Pages[1]; p != s2 {
		t.Fatalf("page1 owner=%d, want %d", p, s2)
	}
}

// Concurrent operations are race-clean and totals are conserved.
func TestConcurrent(t *testing.T) {
	a, _ := New(16, 8, 40)
	const workers = 8
	const rounds = 300
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			s := a.NewSegment()
			rng := newTestRNG(int64(seed)*7919 + 1)
			var held []int
			for i := 0; i < rounds; i++ {
				switch rng() % 10 {
				case 0, 1, 2, 3, 4:
					hint := -1
					if rng()%4 == 0 && len(held) > 0 {
						hint = held[rng()%len(held)]
					}
					if p, err := a.AllocPage(s, hint); err == nil {
						held = append(held, p)
					}
				default:
					if len(held) > 0 {
						j := rng() % len(held)
						if err := a.FreePage(s, held[j]); err == nil {
							held = append(held[:j], held[j+1:]...)
						}
					}
				}
			}
			_ = a.FreeSegment(s)
		}(w)
	}
	wg.Wait()
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	snap := a.Snapshot()
	for p, owner := range snap.Pages {
		if owner != 0 {
			t.Fatalf("page %d still owned by %d after all segments freed", p, owner)
		}
	}
	for _, ev := range snap.Extents {
		if ev.State != StateFree {
			t.Fatalf("extent %d state %s after all frees", ev.ID, ev.State)
		}
	}
}

func newTestRNG(seed int64) func() int {
	state := seed
	return func() int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) & 0x7fffffff)
	}
}
