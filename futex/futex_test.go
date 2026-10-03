package futex

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, w int) *Futex {
	t.Helper()
	f, err := New(w)
	if err != nil {
		t.Fatalf("New(%d): %v", w, err)
	}
	return f
}

func mustWait(t *testing.T, f *Futex, tid, addr int64, expected int32, bitset uint32, prio int, deadline int64) {
	t.Helper()
	if err := f.Wait(tid, addr, expected, bitset, prio, deadline); err != nil {
		t.Fatalf("Wait(tid=%d addr=%d): %v", tid, addr, err)
	}
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got err %v, want %v", what, got, want)
	}
}

func wantIDs(t *testing.T, got []int64, want []int64, what string) {
	t.Helper()
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("%s: got %v, want empty", what, got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func wantWaiters(t *testing.T, f *Futex, addr int64, want []int64) {
	t.Helper()
	got := f.Waiters(addr)
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("Waiters(%d): got %v, want empty", addr, got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waiters(%d): got %v, want %v", addr, got, want)
	}
}

func wantState(t *testing.T, f *Futex, tid int64, state State, addr int64) {
	t.Helper()
	s, a := f.State(tid)
	if s != state || (state == StateWaiting && a != addr) {
		t.Fatalf("State(%d): got (%v, %d), want (%v, %d)", tid, s, a, state, addr)
	}
}

func TestNewRejectsBadCapacity(t *testing.T) {
	for _, w := range []int{0, -1, MaxWaiters + 1} {
		if _, err := New(w); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d): got %v, want ErrInvalidParam", w, err)
		}
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1): %v", err)
	}
	if _, err := New(MaxWaiters); err != nil {
		t.Fatalf("New(MaxWaiters): %v", err)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	f := mustNew(t, 100)
	if err := f.Store(100, 0); err != nil {
		t.Fatalf("Store: %v", err)
	}
	mustWait(t, f, 1, 100, 0, 0x1, 50, 0)
	mustWait(t, f, 2, 100, 0, 0x2, 10, 0)
	mustWait(t, f, 3, 100, 0, 0x1, 50, 0)
	wantWaiters(t, f, 100, []int64{2, 1, 3})

	got, err := f.Wake(100, 1, 0x1)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	wantIDs(t, got, []int64{1}, "Wake(100,1,0x1)")
	wantWaiters(t, f, 100, []int64{2, 3})
	wantState(t, f, 1, StateWoken, 0)

	if err := f.Store(100, 5); err != nil {
		t.Fatalf("Store: %v", err)
	}
	wantErr(t, f.Wait(4, 100, 0, 0x1, 50, 0), ErrValueChanged, "Wait after Store")

	woken, moved, err := f.Requeue(100, 200, 0, 5, true, 5)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	wantIDs(t, woken, nil, "Requeue woken")
	wantIDs(t, moved, []int64{2, 3}, "Requeue moved")
	wantWaiters(t, f, 100, nil)
	wantWaiters(t, f, 200, []int64{2, 3})

	mustWait(t, f, 5, 100, 5, 0x4, 20, 0)
	first, second, err := f.WakeOp(200, 100, 1, 1, OpAdd, 1, CmpEQ, 5)
	if err != nil {
		t.Fatalf("WakeOp: %v", err)
	}
	wantIDs(t, first, []int64{2}, "WakeOp first")
	wantIDs(t, second, []int64{5}, "WakeOp second")
	if v := f.Load(100); v != 6 {
		t.Fatalf("Load(100): got %d, want 6", v)
	}
}

// TestSpecTimeoutExample replays the timeout example from the specification.
func TestSpecTimeoutExample(t *testing.T) {
	f := mustNew(t, 100)
	mustWait(t, f, 6, 300, 0, 0x1, 1, 10)
	mustWait(t, f, 7, 300, 0, 0x1, 1, 10)
	mustWait(t, f, 8, 300, 0, 0x1, 1, 5)

	got, err := f.Advance(9)
	if err != nil {
		t.Fatalf("Advance(9): %v", err)
	}
	wantIDs(t, got, []int64{8}, "Advance(9)")
	wantState(t, f, 8, StateTimedOut, 0)

	got, err = f.Advance(10)
	if err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	wantIDs(t, got, []int64{6, 7}, "Advance(10)")

	wantErr(t, f.Wait(9, 300, 0, 0x1, 1, 10), ErrImmediateTimeout, "Wait deadline<=now")
}

func TestWaitValidationOrder(t *testing.T) {
	f := mustNew(t, 2)
	if err := f.Store(7, 3); err != nil {
		t.Fatalf("Store: %v", err)
	}
	// Invalid parameters win over every other check.
	wantErr(t, f.Wait(-1, 7, 0, 0x1, 0, 0), ErrInvalidParam, "negative tid")
	wantErr(t, f.Wait(1, -1, 0, 0x1, 0, 0), ErrInvalidParam, "negative addr")
	wantErr(t, f.Wait(1, 7, 0, 0x1, 0, -1), ErrInvalidParam, "negative deadline")
	wantErr(t, f.Wait(1, 7, 0, 0, 0, 0), ErrInvalidParam, "zero bitset")
	wantErr(t, f.Wait(1, 7, 0, 0x1, -1, 0), ErrInvalidParam, "negative prio")
	wantErr(t, f.Wait(1, 7, 0, 0x1, 100, 0), ErrInvalidParam, "prio > 99")

	// Value changed beats immediate timeout.
	if _, err := f.Advance(10); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	wantErr(t, f.Wait(1, 7, 0, 0x1, 0, 10), ErrValueChanged, "value before timeout")
	// deadline == now times out immediately; deadline == now+1 is accepted.
	wantErr(t, f.Wait(1, 7, 3, 0x1, 0, 10), ErrImmediateTimeout, "deadline == now")
	mustWait(t, f, 1, 7, 3, 0x1, 0, 11)
	// Busy beats value changed.
	wantErr(t, f.Wait(1, 7, 0, 0x1, 0, 0), ErrBusy, "busy")
	// Queue full is checked last.
	mustWait(t, f, 2, 7, 3, 0x1, 0, 0)
	wantErr(t, f.Wait(3, 7, 3, 0x1, 0, 0), ErrQueueFull, "queue full")
	wantErr(t, f.Wait(3, 7, 0, 0x1, 0, 0), ErrValueChanged, "value before full")
	// Rejected waits changed nothing.
	wantWaiters(t, f, 7, []int64{1, 2})
}

func TestPrioOrderAndFIFO(t *testing.T) {
	f := mustNew(t, 100)
	mustWait(t, f, 1, 50, 0, 0x1, 50, 0)
	mustWait(t, f, 2, 50, 0, 0x1, 10, 0)
	mustWait(t, f, 3, 50, 0, 0x1, 50, 0)
	mustWait(t, f, 4, 50, 0, 0x1, 99, 0)
	mustWait(t, f, 5, 50, 0, 0x1, 0, 0)
	wantWaiters(t, f, 50, []int64{5, 2, 1, 3, 4})
	got, err := f.Wake(50, 2, 0x1)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	wantIDs(t, got, []int64{5, 2}, "wake order")
	wantWaiters(t, f, 50, []int64{1, 3, 4})
}

func TestWakeBitsetSkipsMismatches(t *testing.T) {
	f := mustNew(t, 100)
	mustWait(t, f, 1, 60, 0, 0x1, 10, 0)
	mustWait(t, f, 2, 60, 0, 0x2, 10, 0)
	mustWait(t, f, 3, 60, 0, 0x1, 10, 0)
	f.ResetExamined()
	got, err := f.Wake(60, 1, 0x2)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	wantIDs(t, got, []int64{2}, "wake matching only")
	wantWaiters(t, f, 60, []int64{1, 3})
	// Examined exactly one skipped mismatch plus one woken waiter.
	if e := f.Examined(); e != 2 {
		t.Fatalf("examined: got %d, want 2", e)
	}
	// n == 0 wakes nobody and examines nobody.
	f.ResetExamined()
	got, err = f.Wake(60, 0, 0x1)
	if err != nil {
		t.Fatalf("Wake n=0: %v", err)
	}
	wantIDs(t, got, nil, "wake n=0")
	wantWaiters(t, f, 60, []int64{1, 3})
	if e := f.Examined(); e != 0 {
		t.Fatalf("examined after n=0: got %d, want 0", e)
	}
	_, err = f.Wake(-1, 1, 0x1)
	wantErr(t, err, ErrInvalidParam, "wake negative addr")
	_, err = f.Wake(60, -1, 0x1)
	wantErr(t, err, ErrInvalidParam, "wake negative n")
	_, err = f.Wake(60, 1, 0)
	wantErr(t, err, ErrInvalidParam, "wake zero bitset")
}

func TestRequeueCheckFailureMovesNothing(t *testing.T) {
	f := mustNew(t, 100)
	if err := f.Store(1, 9); err != nil {
		t.Fatalf("Store: %v", err)
	}
	mustWait(t, f, 1, 1, 9, 0x1, 5, 0)
	mustWait(t, f, 2, 1, 9, 0x1, 5, 0)
	// check=true with wrong expected: nothing wakes, nothing moves.
	woken, moved, err := f.Requeue(1, 2, 3, 3, true, 8)
	wantErr(t, err, ErrValueChanged, "requeue check mismatch")
	if woken != nil || moved != nil {
		t.Fatalf("rejected requeue returned %v %v", woken, moved)
	}
	wantWaiters(t, f, 1, []int64{1, 2})
	wantWaiters(t, f, 2, nil)
	wantState(t, f, 1, StateWaiting, 1)
	wantState(t, f, 2, StateWaiting, 1)
	// check=false ignores the word entirely.
	woken, moved, err = f.Requeue(1, 2, 1, 1, false, 0)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	wantIDs(t, woken, []int64{1}, "requeue wake phase")
	wantIDs(t, moved, []int64{2}, "requeue move phase")
	wantState(t, f, 1, StateWoken, 0)
	wantState(t, f, 2, StateWaiting, 2)
	// Invalid parameters are checked before the value check.
	_, _, err = f.Requeue(3, 3, 0, 0, true, 12345)
	wantErr(t, err, ErrInvalidParam, "requeue same addr")
	_, _, err = f.Requeue(-1, 3, 0, 0, true, 12345)
	wantErr(t, err, ErrInvalidParam, "requeue negative addr")
	_, _, err = f.Requeue(1, 3, -1, 0, true, 12345)
	wantErr(t, err, ErrInvalidParam, "requeue negative nWake")
	_, _, err = f.Requeue(1, 3, 0, -1, true, 12345)
	wantErr(t, err, ErrInvalidParam, "requeue negative nRequeue")
}

func TestRequeueAppendsToPrioGroupTail(t *testing.T) {
	f := mustNew(t, 100)
	// Existing waiters on a2 at the same prio.
	mustWait(t, f, 10, 2, 0, 0x1, 5, 0)
	mustWait(t, f, 11, 2, 0, 0x1, 5, 0)
	mustWait(t, f, 12, 2, 0, 0x1, 1, 0)
	// Waiters on a1: tids 1 (prio 5), 2 (prio 5), 3 (prio 7).
	mustWait(t, f, 1, 1, 0, 0x1, 5, 0)
	mustWait(t, f, 2, 1, 0, 0x1, 5, 0)
	mustWait(t, f, 3, 1, 0, 0x1, 7, 0)
	woken, moved, err := f.Requeue(1, 2, 0, 3, false, 0)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	wantIDs(t, woken, nil, "no wake phase")
	wantIDs(t, moved, []int64{1, 2, 3}, "moved in queue order")
	// Moved waiters land at the tail of their prio groups on a2.
	wantWaiters(t, f, 2, []int64{12, 10, 11, 1, 2, 3})
	wantWaiters(t, f, 1, nil)
}

func TestRequeuePreservesDeadlineAndBitset(t *testing.T) {
	f := mustNew(t, 100)
	mustWait(t, f, 1, 1, 0, 0x4, 5, 20)
	mustWait(t, f, 2, 1, 0, 0x1, 5, 0)
	_, moved, err := f.Requeue(1, 2, 0, 2, false, 0)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	wantIDs(t, moved, []int64{1, 2}, "moved")
	// The moved waiter still times out on its original deadline.
	got, err := f.Advance(20)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	wantIDs(t, got, []int64{1}, "timeout after requeue")
	wantState(t, f, 1, StateTimedOut, 0)
	// The moved waiter is still matched by its bitset on the new address.
	got, err = f.Wake(2, 1, 0x1)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	wantIDs(t, got, []int64{2}, "bitset wake after requeue")
}

func TestWakeOpComparesOldValue(t *testing.T) {
	f := mustNew(t, 100)
	if err := f.Store(100, 5); err != nil {
		t.Fatalf("Store: %v", err)
	}
	mustWait(t, f, 1, 100, 5, 0x1, 5, 0)
	mustWait(t, f, 2, 200, 0, 0x1, 5, 0)
	// old=5 becomes 6; EQ 5 holds only when comparing the old value.
	first, second, err := f.WakeOp(200, 100, 1, 1, OpAdd, 1, CmpEQ, 5)
	if err != nil {
		t.Fatalf("WakeOp: %v", err)
	}
	wantIDs(t, first, []int64{2}, "wake a1 head")
	wantIDs(t, second, []int64{1}, "cmp on old value")
	if v := f.Load(100); v != 6 {
		t.Fatalf("Load(100): got %d, want 6", v)
	}
	// OpSet 0 makes the new value 0; cmp EQ 0 would hold for the new
	// value but the old value is 6, so nobody on a2 is woken.
	mustWait(t, f, 3, 100, 6, 0x1, 5, 0)
	mustWait(t, f, 4, 200, 0, 0x1, 5, 0)
	first, second, err = f.WakeOp(200, 100, 1, 1, OpSet, 0, CmpEQ, 0)
	if err != nil {
		t.Fatalf("WakeOp: %v", err)
	}
	wantIDs(t, first, []int64{4}, "wake a1 head")
	wantIDs(t, second, nil, "cmp uses old value 6, not new value 0")
	if v := f.Load(100); v != 0 {
		t.Fatalf("Load(100): got %d, want 0", v)
	}
	wantState(t, f, 3, StateWaiting, 100)
}

func TestWakeOpAllOpsAndWrap(t *testing.T) {
	f := mustNew(t, 100)
	cases := []struct {
		name string
		init int32
		op   Op
		arg  int32
		want int32
	}{
		{"set", 5, OpSet, -7, -7},
		{"add", 5, OpAdd, 3, 8},
		{"add-wrap", math.MaxInt32, OpAdd, 1, math.MinInt32},
		{"add-wrap-neg", math.MinInt32, OpAdd, -1, math.MaxInt32},
		{"or", 0x0F0F, OpOr, 0x00F0, 0x0FFF},
		{"andn", 0xFFFF, OpAndN, 0x0F0F, 0xF0F0},
		{"xor", 0xFF00, OpXor, 0x0FF0, 0xF0F0},
	}
	for i, tc := range cases {
		addr := int64(1000 + i)
		if err := f.Store(addr, tc.init); err != nil {
			t.Fatalf("Store: %v", err)
		}
		if _, _, err := f.WakeOp(addr, addr, 0, 0, tc.op, tc.arg, CmpNE, tc.want); err != nil {
			t.Fatalf("%s: WakeOp: %v", tc.name, err)
		}
		if v := f.Load(addr); v != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, v, tc.want)
		}
	}
}

func TestWakeOpCmpFalseStillWrites(t *testing.T) {
	f := mustNew(t, 100)
	if err := f.Store(2, 10); err != nil {
		t.Fatalf("Store: %v", err)
	}
	mustWait(t, f, 1, 1, 0, 0x1, 5, 0)
	mustWait(t, f, 2, 2, 10, 0x1, 5, 0)
	first, second, err := f.WakeOp(1, 2, 1, 1, OpAdd, 5, CmpGT, 100)
	if err != nil {
		t.Fatalf("WakeOp: %v", err)
	}
	wantIDs(t, first, []int64{1}, "a1 woken unconditionally")
	wantIDs(t, second, nil, "cmp false: a2 untouched")
	if v := f.Load(2); v != 15 {
		t.Fatalf("Load(2): got %d, want 15 (word rewritten even when cmp fails)", v)
	}
	wantState(t, f, 2, StateWaiting, 2)
}

func TestWakeOpSameAddress(t *testing.T) {
	f := mustNew(t, 100)
	for i := int64(1); i <= 5; i++ {
		mustWait(t, f, i, 9, 0, 0x1, 5, 0)
	}
	first, second, err := f.WakeOp(9, 9, 2, 2, OpAdd, 0, CmpEQ, 0)
	if err != nil {
		t.Fatalf("WakeOp: %v", err)
	}
	wantIDs(t, first, []int64{1, 2}, "first n1")
	wantIDs(t, second, []int64{3, 4}, "second n2 continues on the rest")
	wantWaiters(t, f, 9, []int64{5})
}

func TestWakeOpInvalidParams(t *testing.T) {
	f := mustNew(t, 100)
	if err := f.Store(5, 5); err != nil {
		t.Fatalf("Store: %v", err)
	}
	before := f.Load(5)
	for _, bad := range []struct {
		a1, a2 int64
		n1, n2 int
		op     Op
		cmp    Cmp
	}{
		{-1, 5, 0, 0, OpSet, CmpEQ},
		{5, -1, 0, 0, OpSet, CmpEQ},
		{5, 5, -1, 0, OpSet, CmpEQ},
		{5, 5, 0, -1, OpSet, CmpEQ},
		{5, 5, 0, 0, Op(99), CmpEQ},
		{5, 5, 0, 0, OpSet, Cmp(99)},
		{5, 5, 0, 0, Op(-1), CmpEQ},
		{5, 5, 0, 0, OpSet, Cmp(-1)},
	} {
		if _, _, err := f.WakeOp(bad.a1, bad.a2, bad.n1, bad.n2, bad.op, 1, bad.cmp, 1); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("WakeOp(%+v): got %v, want ErrInvalidParam", bad, err)
		}
	}
	if v := f.Load(5); v != before {
		t.Fatalf("rejected WakeOp changed the word: %d -> %d", before, v)
	}
}

func TestAdvanceOrderBoundariesAndErrors(t *testing.T) {
	f := mustNew(t, 100)
	// Same deadline: seq order. Different deadlines: deadline order.
	mustWait(t, f, 1, 1, 0, 0x1, 5, 10)
	mustWait(t, f, 2, 2, 0, 0x1, 5, 5)
	mustWait(t, f, 3, 1, 0, 0x1, 5, 10)
	mustWait(t, f, 4, 1, 0, 0x1, 5, 0) // no deadline
	// Advance below the earliest deadline: nobody times out.
	got, err := f.Advance(4)
	if err != nil {
		t.Fatalf("Advance(4): %v", err)
	}
	wantIDs(t, got, nil, "nothing due")
	// Advance exactly to a deadline: it fires (deadline <= now).
	got, err = f.Advance(5)
	if err != nil {
		t.Fatalf("Advance(5): %v", err)
	}
	wantIDs(t, got, []int64{2}, "deadline == now fires")
	// One short of the next deadline: no timeout.
	got, err = f.Advance(9)
	if err != nil {
		t.Fatalf("Advance(9): %v", err)
	}
	wantIDs(t, got, nil, "deadline-1 does not fire")
	got, err = f.Advance(10)
	if err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	wantIDs(t, got, []int64{1, 3}, "same deadline in seq order")
	wantWaiters(t, f, 1, []int64{4})
	// Errors: negative now, then going backward.
	if _, err := f.Advance(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Advance(-1): got %v, want ErrInvalidParam", err)
	}
	if _, err := f.Advance(9); !errors.Is(err, ErrTimeBackward) {
		t.Fatalf("Advance(9): got %v, want ErrTimeBackward", err)
	}
	// Rejected advances do not move the clock.
	got, err = f.Advance(10)
	if err != nil {
		t.Fatalf("Advance(10): %v", err)
	}
	wantIDs(t, got, nil, "clock unchanged")
}

func TestCancel(t *testing.T) {
	f := mustNew(t, 100)
	mustWait(t, f, 1, 1, 0, 0x1, 5, 30)
	mustWait(t, f, 2, 1, 0, 0x1, 5, 0)
	if err := f.Cancel(1); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	wantState(t, f, 1, StateInterrupted, 0)
	wantWaiters(t, f, 1, []int64{2})
	// The cancelled waiter's deadline is forgotten.
	got, err := f.Advance(30)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	wantIDs(t, got, nil, "cancelled waiter does not time out")
	// Errors.
	wantErr(t, f.Cancel(-1), ErrInvalidParam, "negative tid")
	wantErr(t, f.Cancel(1), ErrNotWaiting, "not waiting anymore")
	wantErr(t, f.Cancel(99), ErrNotWaiting, "unknown tid")
	// A cancelled thread may wait again.
	mustWait(t, f, 1, 1, 0, 0x1, 5, 0)
	wantState(t, f, 1, StateWaiting, 1)
}

func TestQueueFull(t *testing.T) {
	f := mustNew(t, 2)
	mustWait(t, f, 1, 1, 0, 0x1, 5, 0)
	mustWait(t, f, 2, 2, 0, 0x1, 5, 0)
	wantErr(t, f.Wait(3, 3, 0, 0x1, 5, 0), ErrQueueFull, "third waiter")
	if _, err := f.Wake(1, 1, 0x1); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	mustWait(t, f, 3, 3, 0, 0x1, 5, 0)
}

func TestRejectedOpsChangeNothing(t *testing.T) {
	f := mustNew(t, 3)
	if err := f.Store(1, 7); err != nil {
		t.Fatalf("Store: %v", err)
	}
	mustWait(t, f, 1, 1, 7, 0x1, 5, 50)
	snapshot := func() (int32, []int64, State) {
		st, _ := f.State(1)
		return f.Load(1), f.Waiters(1), st
	}
	mem0, q0, st0 := snapshot()
	// A batch of rejected operations.
	wantErr(t, f.Wait(2, 1, 0, 0x1, 5, 0), ErrValueChanged, "wait mismatch")
	wantErr(t, f.Wait(2, 1, 7, 0, 5, 0), ErrInvalidParam, "wait zero bitset")
	wantErr(t, f.Cancel(2), ErrNotWaiting, "cancel idle")
	if _, err := f.Wake(-1, 1, 0x1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("wake bad addr")
	}
	if _, _, err := f.Requeue(1, 1, 1, 1, false, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("requeue same addr")
	}
	if _, _, err := f.Requeue(1, 2, 1, 1, true, 8); !errors.Is(err, ErrValueChanged) {
		t.Fatalf("requeue check")
	}
	if _, _, err := f.WakeOp(1, 1, 1, 1, Op(42), 0, CmpEQ, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("wakeop bad op")
	}
	if _, err := f.Advance(1000); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	// After the (accepted) advance the waiter timed out; requeue a fresh
	// waiter and reject operations to verify no state drift.
	mem1, q1, st1 := snapshot()
	if mem1 != mem0 {
		t.Fatalf("memory changed by rejected ops: %d -> %d", mem0, mem1)
	}
	if st0 != StateWaiting {
		t.Fatalf("state before advance: got %v, want waiting", st0)
	}
	if st1 != StateTimedOut {
		t.Fatalf("state: got %v, want timedout (advance was accepted)", st1)
	}
	if len(q1) != 0 || len(q0) != 1 {
		t.Fatalf("queue drift: %v -> %v", q0, q1)
	}
}
