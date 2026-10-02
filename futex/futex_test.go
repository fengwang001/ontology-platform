package futex

import (
	"errors"
	"reflect"
	"testing"
)

func mustWait(t *testing.T, f *Futex, tid int, addr int64, expected int32, bitset uint32, prio int, deadline int64) {
	t.Helper()
	if err := f.Wait(tid, addr, expected, bitset, prio, deadline); err != nil {
		t.Fatalf("Wait(%d,@%d) unexpected error: %v", tid, addr, err)
	}
}

func TestNewBounds(t *testing.T) {
	for _, w := range []int{1, 1_000_000} {
		f := New(w)
		if f.cap != w {
			t.Fatalf("cap=%d want %d", f.cap, w)
		}
	}
	for _, w := range []int{0, -1, 1_000_001} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("New(%d) should be rejected", w)
				}
			}()
			New(w)
		}()
	}
}

func eqInts(t *testing.T, name string, got, want []int) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func assertErrIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	f := New(1000)
	f.Store(100, 0)
	mustWait(t, f, 1, 100, 0, 0x1, 50, 0)
	mustWait(t, f, 2, 100, 0, 0x2, 10, 0)
	mustWait(t, f, 3, 100, 0, 0x1, 50, 0)
	eqInts(t, "queue@100", f.Waiters(100), []int{2, 1, 3})

	w, err := f.Wake(100, 1, 0x1)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wake", w, []int{1})
	eqInts(t, "queue@100", f.Waiters(100), []int{2, 3})

	f.Store(100, 5)
	assertErrIs(t, f.Wait(4, 100, 0, 0x1, 50, 0), ErrChanged)

	wk, rq, err := f.Requeue(100, 200, 0, 5, true, 5)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "requeue-woken", wk, nil)
	eqInts(t, "requeue-moved", rq, []int{2, 3})
	eqInts(t, "queue@200", f.Waiters(200), []int{2, 3})

	mustWait(t, f, 5, 100, 5, 0x4, 20, 0)
	w1, w2, err := f.WakeOp(200, 100, 1, 1, OpAdd, 1, CmpEq, 5)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wakeop-w1", w1, []int{2})
	eqInts(t, "wakeop-w2", w2, []int{5})
	if f.Load(100) != 6 {
		t.Fatalf("Load(100)=%d, want 6", f.Load(100))
	}

	// Timeout example.
	mustWait(t, f, 6, 300, 0, 0x1, 1, 10)
	mustWait(t, f, 7, 300, 0, 0x1, 1, 10)
	mustWait(t, f, 8, 300, 0, 0x1, 1, 5)
	ex, err := f.Advance(9)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "advance(9)", ex, []int{8})
	ex, err = f.Advance(10)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "advance(10)", ex, []int{6, 7})
	assertErrIs(t, f.Wait(9, 300, 0, 0x1, 1, 10), ErrTimeout)
}

func TestWaitValidationOrder(t *testing.T) {
	f := New(2)
	assertErrIs(t, f.Wait(-1, 0, 0, 1, 0, 0), ErrInvalid)
	assertErrIs(t, f.Wait(0, -1, 0, 1, 0, 0), ErrInvalid)
	assertErrIs(t, f.Wait(0, 0, 0, 0, 0, 0), ErrInvalid)
	assertErrIs(t, f.Wait(0, 0, 0, 1, -1, 0), ErrInvalid)
	assertErrIs(t, f.Wait(0, 0, 0, 1, 100, 0), ErrInvalid)
	assertErrIs(t, f.Wait(0, 0, 0, 1, 0, -1), ErrInvalid)

	// Busy precedes changed/timeout/full.
	mustWait(t, f, 0, 10, 0, 1, 0, 0)
	f.Store(10, 9)
	assertErrIs(t, f.Wait(0, 10, 0, 1, 0, 0), ErrBusy)
	if err := f.Cancel(0); err != nil {
		t.Fatal(err)
	}

	// Changed precedes immediate-timeout.
	_, _ = f.Advance(5)
	assertErrIs(t, f.Wait(1, 10, 0, 1, 0, 5), ErrChanged)

	// Immediate-timeout (deadline == now) precedes full.
	mustWait(t, f, 2, 20, 0, 1, 0, 0)
	mustWait(t, f, 3, 20, 0, 1, 0, 0)
	assertErrIs(t, f.Wait(4, 20, 0, 1, 0, 5), ErrTimeout)
	// Capacity still blocks even though the deadline is in the future.
	assertErrIs(t, f.Wait(4, 20, 0, 1, 0, 6), ErrFull)
	// On a free address, deadline == now+1 enqueues (one unit of slack).
	if err := f.Cancel(2); err != nil {
		t.Fatal(err)
	}
	mustWait(t, f, 4, 21, 0, 1, 0, 6)
}

func TestPriorityOrdering(t *testing.T) {
	f := New(100)
	mustWait(t, f, 1, 0, 0, 1, 50, 0)
	mustWait(t, f, 2, 0, 0, 1, 10, 0)
	mustWait(t, f, 3, 0, 0, 1, 50, 0)
	mustWait(t, f, 4, 0, 0, 1, 10, 0)
	mustWait(t, f, 5, 0, 0, 1, 30, 0)
	eqInts(t, "queue", f.Waiters(0), []int{2, 4, 5, 1, 3})
}

func TestBitsetWake(t *testing.T) {
	f := New(100)
	mustWait(t, f, 1, 0, 0, 0x1, 0, 0)
	mustWait(t, f, 2, 0, 0, 0x2, 0, 0)
	mustWait(t, f, 3, 0, 0, 0x3, 0, 0)
	mustWait(t, f, 4, 0, 0, 0x2, 0, 0)

	w, err := f.Wake(0, 2, 0x1)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wake", w, []int{1, 3})
	eqInts(t, "queue", f.Waiters(0), []int{2, 4})

	w, err = f.Wake(0, 0, 0xFFFFFFFF)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wake n=0", w, nil)
	eqInts(t, "queue", f.Waiters(0), []int{2, 4})

	_, err = f.Wake(0, 1, 0)
	assertErrIs(t, err, ErrInvalid)
	_, err = f.Wake(-1, 1, 1)
	assertErrIs(t, err, ErrInvalid)
}
