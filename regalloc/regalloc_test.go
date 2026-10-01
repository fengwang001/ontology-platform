package regalloc

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, k int) *Allocator {
	t.Helper()
	a, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	return a
}

func mustAdd(t *testing.T, a *Allocator, id, start, end int) Assignment {
	t.Helper()
	asg, err := a.Add(id, start, end)
	if err != nil {
		t.Fatalf("Add(%d, %d, %d): %v", id, start, end, err)
	}
	t.Logf("Add(id=%d, [%d,%d)) -> %+v", id, start, end, asg)
	return asg
}

func mustQuery(t *testing.T, a *Allocator, id int) Assignment {
	t.Helper()
	asg, err := a.Query(id)
	if err != nil {
		t.Fatalf("Query(%d): %v", id, err)
	}
	return asg
}

func wantReg(t *testing.T, asg Assignment, reg int) {
	t.Helper()
	if asg.Spilled || asg.Register != reg {
		t.Fatalf("want register %d, got %+v", reg, asg)
	}
}

func wantSlot(t *testing.T, asg Assignment, slot int) {
	t.Helper()
	if !asg.Spilled || asg.Slot != slot {
		t.Fatalf("want spill slot %d, got %+v", slot, asg)
	}
}

// An interval whose end equals the new start is already dead; its
// register is reused immediately.
func TestExpireAtExactStart(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 5), 0)
	// end(1)=5 <= start(2)=5: interval 1 expires, register 0 is reused.
	wantReg(t, mustAdd(t, a, 2, 5, 7), 0)
	wantReg(t, mustAdd(t, a, 3, 7, 9), 0)
	wantReg(t, mustQuery(t, a, 1), 0)
	wantReg(t, mustQuery(t, a, 2), 0)
	wantReg(t, mustQuery(t, a, 3), 0)
}

// end > new start means NOT expired, even by one unit.
func TestNoExpireBeforeEnd(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 5), 0)
	// end(1)=5 > start(2)=4: interval 1 still lives; new end 6 >= 5,
	// so the new interval spills itself.
	wantSlot(t, mustAdd(t, a, 2, 4, 6), 0)
	wantReg(t, mustQuery(t, a, 1), 0)
	wantSlot(t, mustQuery(t, a, 2), 0)
}

// On an end tie between the new interval and an active one, the new
// interval spills itself.
func TestTieSpillsNewInterval(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 10), 0)
	// end(2)=10 ties end(1)=10: the new interval is spilled.
	wantSlot(t, mustAdd(t, a, 2, 5, 10), 0)
	wantReg(t, mustQuery(t, a, 1), 0)
	wantSlot(t, mustQuery(t, a, 2), 0)
}

// On an end tie among active intervals, the smallest id is spilled.
func TestActiveTieSpillsSmallestID(t *testing.T) {
	a := mustNew(t, 2)
	wantReg(t, mustAdd(t, a, 5, 0, 10), 0)
	wantReg(t, mustAdd(t, a, 3, 1, 10), 1)
	// Both actives end at 10; new end 8 < 10, so an active spills:
	// the tie is broken by smallest id -> interval 3.
	wantReg(t, mustAdd(t, a, 9, 2, 8), 1)
	wantReg(t, mustQuery(t, a, 5), 0)
	wantSlot(t, mustQuery(t, a, 3), 0)
	wantReg(t, mustQuery(t, a, 9), 1)
}

// K=1: each new shorter interval evicts the previous holder; spill
// slots are handed out 0, 1, 2, ... with no gaps.
func TestK1ConsecutiveSpills(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 10), 0)
	wantReg(t, mustAdd(t, a, 2, 1, 5), 0)  // 1 spilled to slot 0
	wantReg(t, mustAdd(t, a, 3, 2, 4), 0)  // 2 spilled to slot 1
	wantSlot(t, mustAdd(t, a, 4, 3, 4), 2) // end tie 4 == 4: new interval spills itself
	wantSlot(t, mustQuery(t, a, 1), 0)
	wantSlot(t, mustQuery(t, a, 2), 1)
	wantReg(t, mustQuery(t, a, 3), 0)
	wantSlot(t, mustQuery(t, a, 4), 2)
}

// A spilled interval's register is reused by later intervals, while
// the spilled interval keeps reporting its slot.
func TestSpilledRegisterReused(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 10), 0)
	wantReg(t, mustAdd(t, a, 2, 1, 5), 0) // 1 spilled to slot 0, 2 takes reg 0
	// end(2)=5 <= start(3)=5: 2 expires, register 0 free again.
	wantReg(t, mustAdd(t, a, 3, 5, 8), 0)
	wantSlot(t, mustQuery(t, a, 1), 0)
	wantReg(t, mustQuery(t, a, 2), 0)
	wantReg(t, mustQuery(t, a, 3), 0)
}

func TestNewRejectsSmallK(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		if _, err := New(k); !errors.Is(err, ErrInvalidRegisterCount) {
			t.Fatalf("New(%d): want ErrInvalidRegisterCount, got %v", k, err)
		}
	}
}

// Validation order: duplicate id, then start >= end, then regression.
// Rejected adds change nothing.
func TestValidationOrderAndAtomicity(t *testing.T) {
	a := mustNew(t, 1)
	wantReg(t, mustAdd(t, a, 1, 0, 5), 0)
	wantReg(t, mustAdd(t, a, 2, 10, 12), 0)

	// Duplicate id wins over a bad range and a regression.
	if _, err := a.Add(1, 9, 3); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want ErrDuplicateID, got %v", err)
	}
	// Bad range wins over a regression.
	if _, err := a.Add(3, 4, 4); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}
	if _, err := a.Add(3, 6, 2); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}
	// Regression: start 9 < last accepted start 10.
	if _, err := a.Add(3, 9, 11); !errors.Is(err, ErrStartRegression) {
		t.Fatalf("want ErrStartRegression, got %v", err)
	}
	// Equal start is allowed (non-decreasing).
	wantSlot(t, mustAdd(t, a, 3, 10, 20), 0) // 20 >= 12: new interval spills

	// The rejections above consumed no spill slots: this is slot 1,
	// not a higher number, and interval 2 still holds register 0.
	wantSlot(t, mustAdd(t, a, 4, 10, 30), 1)
	wantReg(t, mustQuery(t, a, 2), 0)
	wantSlot(t, mustQuery(t, a, 3), 0)
	wantSlot(t, mustQuery(t, a, 4), 1)
}

func TestQueryUnknownID(t *testing.T) {
	a := mustNew(t, 1)
	mustAdd(t, a, 1, 0, 5)
	_, err := a.Query(99)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// Distinguishable from the other reasons.
	if errors.Is(err, ErrDuplicateID) || errors.Is(err, ErrInvalidRange) ||
		errors.Is(err, ErrStartRegression) || errors.Is(err, ErrInvalidRegisterCount) {
		t.Fatal("ErrNotFound must be distinguishable from ErrDuplicateID")
	}
}

// Replaying the same add sequence yields identical assignments.
func TestDeterministicReplay(t *testing.T) {
	seq := [][3]int{
		{1, 0, 10}, {2, 1, 5}, {3, 1, 7}, {4, 5, 6},
		{5, 6, 9}, {6, 6, 8}, {7, 9, 12}, {8, 9, 10},
	}
	run := func() []Assignment {
		a := mustNew(t, 2)
		out := make([]Assignment, 0, len(seq))
		for _, iv := range seq {
			out = append(out, mustAdd(t, a, iv[0], iv[1], iv[2]))
		}
		for _, iv := range seq {
			out = append(out, mustQuery(t, a, iv[0]))
		}
		return out
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %v: %+v vs %+v", seq[i], first[i], second[i])
		}
	}
}
