package broadphase

import (
	"errors"
	"sync"
	"testing"
)

func TestEdgeContactsAndRefatConditions(t *testing.T) {
	bp, err := New(1, 10)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bp.Insert(1, box(0, 1, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if events, err := bp.Insert(2, box(3, 4, 0, 2)); err != nil {
		t.Fatal(err)
	} else if len(events.FatEnter) != 0 || len(events.ContactEnter) != 0 {
		t.Fatalf("edge-touching tight box produced events: %+v", events)
	}

	bp2, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp2.Insert(1, box(0, 4, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp2.Insert(2, box(10, 12, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if moved, err := bp2.Move(2, box(12, 14, 0, 4)); err != nil || moved.Refatted {
		t.Fatalf("hi equal to fat hi: %+v %v", moved, err)
	}
	if moved, err := bp2.Move(2, box(12, 15, 0, 4)); err != nil || !moved.Refatted {
		t.Fatalf("hi one larger should refat: %+v %v", moved, err)
	}
}

func TestNonRefatContactMove(t *testing.T) {
	bp, err := New(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 4, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(10, 14, 0, 4)); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(2, box(6, 10, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if moved.Refatted || len(moved.FatEnter) != 0 || len(moved.ContactEnter) != 0 || bp.crossed != 0 || bp.checks != 0 {
		t.Fatalf("edge contact move result=%+v counters=%d/%d", moved, bp.crossed, bp.checks)
	}
	if bp.contactChecks != len(bp.neighbors[2]) {
		t.Fatalf("contact checks=%d, fat neighbor count=%d", bp.contactChecks, len(bp.neighbors[2]))
	}
	moved, err = bp.Move(2, box(3, 7, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if moved.Refatted || len(moved.FatEnter) != 0 || !reflectPairList(moved.ContactEnter, Pair{1, 2}) {
		t.Fatalf("contact enter without fat event failed: %+v", moved)
	}
	moved, err = bp.Move(2, box(4, 8, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if moved.Refatted || !reflectPairList(moved.ContactExit, Pair{1, 2}) {
		t.Fatalf("contact exit without fat event failed: %+v", moved)
	}
}

func reflectPairList(pairs []Pair, want Pair) bool {
	return len(pairs) == 1 && pairs[0] == want
}

func TestRefatDoesNotTakeUnion(t *testing.T) {
	bp, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 4, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(8, 10, 0, 4)); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(2, box(4, 6, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || !reflectPairList(moved.FatEnter, Pair{1, 2}) {
		t.Fatalf("expected fat enter, got %+v", moved)
	}
	moved, err = bp.Move(2, box(12, 14, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || !reflectPairList(moved.FatExit, Pair{1, 2}) {
		t.Fatalf("union would preserve pair, got %+v", moved)
	}
}

func TestMoveBothEnterAndExit(t *testing.T) {
	bp, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 2, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(1, 3, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(3, box(9, 11, 0, 2)); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(2, box(6, 10, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !reflectPairList(moved.FatExit, Pair{1, 2}) || !reflectPairList(moved.FatEnter, Pair{2, 3}) {
		t.Fatalf("simultaneous enter/exit failed: %+v", moved)
	}
}

func TestBidirectionalFiltersAndZero(t *testing.T) {
	bp, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 4, 0, 4), 1, 1); err != nil {
		t.Fatal(err)
	}
	if events, err := bp.Insert(2, box(2, 6, 0, 4), 2, 1); err != nil {
		t.Fatal(err)
	} else if len(events.FatEnter) != 0 {
		t.Fatalf("one-way filter passed: %+v", events)
	}
	if events, err := bp.SetFilter(1, 1, 2); err != nil {
		t.Fatal(err)
	} else if !reflectPairList(events.FatEnter, Pair{1, 2}) {
		t.Fatalf("two-way filter failed: %+v", events)
	}
	if events, err := bp.SetFilter(2, 0, 1); err != nil {
		t.Fatal(err)
	} else if !reflectPairList(events.FatExit, Pair{1, 2}) {
		t.Fatalf("layer zero should exit: %+v", events)
	}
	if _, err := bp.SetFilter(2, 2, 0); err != nil {
		t.Fatal(err)
	}
	if events, err := bp.SetFilter(1, 1, 2); err != nil {
		t.Fatal(err)
	} else if len(events.FatEnter) != 0 {
		t.Fatalf("mask zero should not pass: %+v", events)
	}
}

func TestFilterDoesNotCountCrossedAndRejectsAreAtomic(t *testing.T) {
	bp, err := New(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 2, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(1, 3, 1, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Move(1, box(0, 1, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if events, err := bp.SetFilter(1, 0, 1); err != nil {
		t.Fatal(err)
	} else if bp.crossed != 0 || bp.checks != 0 || len(events.FatExit) != 1 {
		t.Fatalf("SetFilter counters/events wrong: %+v %d/%d", events, bp.crossed, bp.checks)
	}
	beforePairs := bp.Pairs()
	beforeContacts := bp.Contacts()

	for _, call := range []func() error{
		func() error { _, err := bp.Insert(0, box(0, 1, 0, 1)); return err },
		func() error { _, err := bp.Insert(1, box(0, 1, 0, 1)); return err },
		func() error { _, err := bp.Insert(3, box(0, 1, 0, 1)); return err },
		func() error { _, err := bp.Move(9, box(0, 1, 0, 1)); return err },
		func() error { _, err := bp.Remove(9); return err },
		func() error { _, err := bp.SetFilter(9, 1, 1); return err },
		func() error { _, err := bp.Move(1, box(1, 0, 0, 1)); return err },
		func() error { _, err := bp.SetFilter(1, 65536, 0); return err },
	} {
		if err := call(); err == nil {
			t.Fatal("rejected operation returned nil")
		}
	}
	if !samePairSlices(bp.Pairs(), beforePairs) || !samePairSlices(bp.Contacts(), beforeContacts) {
		t.Fatal("rejected operations changed state")
	}
}

func samePairSlices(left, right []Pair) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestMarginZeroNonRefatShrinkDoesNotRefat(t *testing.T) {
	bp, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(1, box(1, 9, 1, 9))
	if err != nil || moved.Refatted {
		t.Fatalf("M=0 shrink should not refat: %+v %v", moved, err)
	}
}

func TestNonCollidableFlipHasNoPairCheck(t *testing.T) {
	bp, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(0, 2, 0, 2), 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(12, 14, 0, 2), 2, 1); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(1, box(11, 13, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || bp.crossed != 1 || bp.checks != 0 {
		t.Fatalf("non-collidable flip crossed=%d checks=%d events=%+v", bp.crossed, bp.checks, moved.Events)
	}
}

func TestPairChecksCountEachOppositeFlip(t *testing.T) {
	bp, err := New(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(1, box(10, 11, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := bp.Insert(2, box(20, 21, 0, 2)); err != nil {
		t.Fatal(err)
	}
	moved, err := bp.Move(1, box(30, 31, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if bp.crossed != 4 || bp.checks != 2 {
		t.Fatalf("crossed=%d checks=%d events=%+v, want 4/2", bp.crossed, bp.checks, moved.Events)
	}
}

func TestHundredThousandSmallRefatHasConstantCost(t *testing.T) {
	bp, err := New(1, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 100_000; id++ {
		base := id * 100
		if _, err := bp.Insert(id, box(base, base+2, 0, 2)); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := bp.Move(50_000, box(5_000_000-1, 5_000_000+1, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if moved.Refatted || bp.crossed != 0 || bp.checks != 0 {
		t.Fatalf("inside-fat move had scan cost: %+v %d/%d", moved, bp.crossed, bp.checks)
	}
	moved, err = bp.Move(50_000, box(5_000_000-3, 5_000_000-1, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.Refatted || bp.crossed > 4 || bp.checks > bp.crossed {
		t.Fatalf("small far-ish refat cost crossed=%d checks=%d", bp.crossed, bp.checks)
	}
}

func TestConcurrentOperationsAreSerialized(t *testing.T) {
	bp, err := New(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 200; i++ {
				id := int64(worker*200 + i + 1)
				_, _ = bp.Insert(id, box(0, 2, 0, 2))
				_, _ = bp.Move(id, box(0, 3, 0, 3))
				_, _ = bp.SetFilter(id, 1, 1)
				_ = bp.Pairs()
				_ = bp.Contacts()
				_, _ = bp.Remove(id)
			}
		}(worker)
	}
	wait.Wait()
	if len(bp.Pairs()) != 0 || len(bp.Contacts()) != 0 || bp.count != 0 {
		t.Fatalf("state not empty after concurrent lifecycle: pairs=%d contacts=%d count=%d",
			len(bp.Pairs()), len(bp.Contacts()), bp.count)
	}
}

func TestSentinelErrorsRemainDistinguishable(t *testing.T) {
	cases := []error{ErrInvalidArgument, ErrObjectExists, ErrObjectNotFound, ErrCapacityReached}
	for i := range cases {
		for j := i + 1; j < len(cases); j++ {
			if errors.Is(cases[i], cases[j]) {
				t.Fatalf("errors %v and %v are not distinguishable", cases[i], cases[j])
			}
		}
	}
}
