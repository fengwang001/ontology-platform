package futex

import (
	"errors"
	"testing"
)

func TestAdvanceOrdering(t *testing.T) {
	f := New(100)
	mustWait(t, f, 6, 300, 0, 1, 1, 10)
	mustWait(t, f, 7, 300, 0, 1, 1, 10)
	mustWait(t, f, 8, 300, 0, 1, 1, 5)
	ex, err := f.Advance(9)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "advance(9)", ex, []int{8})

	// deadline == now expires; one unit of slack does not.
	mustWait(t, f, 10, 302, 0, 1, 1, 11)
	mustWait(t, f, 9, 301, 0, 1, 1, 15)
	ex, err = f.Advance(10)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "advance(10): seq tie order", ex, []int{6, 7})
	eqInts(t, "still waiting @302", f.Waiters(302), []int{10})

	ex, err = f.Advance(20)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "advance(20)", ex, []int{10, 9})

	_, err = f.Advance(-1)
	assertErrIs(t, err, ErrInvalid)
	_, err = f.Advance(19)
	assertErrIs(t, err, ErrClockBack)

	// A rejected immediate-timeout Wait does not enqueue nor change state.
	assertErrIs(t, f.Wait(11, 300, 0, 1, 0, 20), ErrTimeout)
	if st, _, _ := f.StateOf(11); st != StateIdle {
		t.Fatalf("tid11 state=%v, want idle", st)
	}
}

func TestCancelAndState(t *testing.T) {
	f := New(100)
	assertErrIs(t, f.Cancel(-1), ErrInvalid)
	assertErrIs(t, f.Cancel(1), ErrNotWait)

	mustWait(t, f, 1, 100, 0, 1, 0, 100)
	mustWait(t, f, 2, 100, 0, 1, 0, 100)
	if err := f.Cancel(1); err != nil {
		t.Fatal(err)
	}
	eqInts(t, "queue after cancel", f.Waiters(100), []int{2})
	if st, _, _ := f.StateOf(1); st != StateInterrupted {
		t.Fatalf("tid1 state=%v", st)
	}
	assertErrIs(t, f.Cancel(1), ErrNotWait)

	ex, err := f.Advance(200)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "timeout", ex, []int{2})
	if st, _, _ := f.StateOf(2); st != StateTimedOut {
		t.Fatalf("tid2 state=%v", st)
	}

	// A successful Wait resets prior state.
	mustWait(t, f, 2, 100, 0, 1, 0, 0)
	if st, addr, _ := f.StateOf(2); st != StateWaiting || addr != 100 {
		t.Fatalf("tid2 after rewait: %v @%d", st, addr)
	}
	w, err := f.Wake(100, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wake", w, []int{2})
	if st, _, _ := f.StateOf(2); st != StateWoken {
		t.Fatalf("tid2 state=%v", st)
	}
	if st, _, _ := f.StateOf(999); st != StateIdle {
		t.Fatalf("unknown tid state=%v", st)
	}
}

func TestRejectedOpsNoMutation(t *testing.T) {
	f := New(1)
	f.Store(100, 5)
	mustWait(t, f, 1, 100, 5, 1, 0, 100)

	if err := f.Wait(2, 100, 5, 0, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bitset 0: %v", err)
	}
	if err := f.Wait(2, 100, 5, 1, 0, 50); !errors.Is(err, ErrFull) {
		t.Fatalf("full: %v", err)
	}
	if _, err := f.Wake(100, -1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative n: %v", err)
	}
	if _, _, err := f.Requeue(100, 200, 0, 1, true, 4); !errors.Is(err, ErrChanged) {
		t.Fatalf("check mismatch: %v", err)
	}
	if _, _, err := f.WakeOp(100, 200, 0, 0, Op(7), 1, CmpEq, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad op: %v", err)
	}
	if _, err := f.Advance(-1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative advance: %v", err)
	}

	if f.Load(100) != 5 {
		t.Fatalf("memory mutated: %d", f.Load(100))
	}
	eqInts(t, "queue unchanged", f.Waiters(100), []int{1})
	if st, addr, _ := f.StateOf(1); st != StateWaiting || addr != 100 {
		t.Fatalf("state mutated: %v @%d", st, addr)
	}
	if f.Now() != 0 {
		t.Fatalf("clock mutated: %d", f.Now())
	}
	// The queue/clock state is fully intact after all the rejected calls.
	if err := f.Cancel(1); err != nil {
		t.Fatal(err)
	}
	mustWait(t, f, 2, 100, 5, 1, 0, 0)
	eqInts(t, "tid2 enqueued fine", f.Waiters(100), []int{2})
}
