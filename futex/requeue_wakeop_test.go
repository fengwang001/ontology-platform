package futex

import "testing"

func TestRequeue(t *testing.T) {
	f := New(100)
	// a2 already has one prio-10 waiter before the requeue.
	mustWait(t, f, 10, 2, 0, 0x1, 10, 0)
	mustWait(t, f, 1, 1, 0, 0x1, 10, 0)
	mustWait(t, f, 2, 1, 0, 0x2, 20, 0)
	mustWait(t, f, 3, 1, 0, 0x4, 10, 0)
	f.Store(1, 7)

	// check mismatch: nobody moves or wakes.
	wk, rq, err := f.Requeue(1, 2, 1, 2, true, 6)
	assertErrIs(t, err, ErrChanged)
	if wk != nil || rq != nil {
		t.Fatalf("failed requeue returned lists: %v %v", wk, rq)
	}
	eqInts(t, "a1 unchanged", f.Waiters(1), []int{1, 3, 2})
	eqInts(t, "a2 unchanged", f.Waiters(2), []int{10})

	wk, rq, err = f.Requeue(1, 2, 1, 5, true, 7)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "woken", wk, []int{1})
	eqInts(t, "moved", rq, []int{3, 2})
	// 3 (prio 10) lands after the existing prio-10 waiter; 2 (prio 20) after.
	eqInts(t, "a2", f.Waiters(2), []int{10, 3, 2})

	_, _, err = f.Requeue(1, 1, 0, 0, false, 0)
	assertErrIs(t, err, ErrInvalid)
	_, _, err = f.Requeue(1, 2, -1, 0, false, 0)
	assertErrIs(t, err, ErrInvalid)
	_, _, err = f.Requeue(-1, 2, 0, 0, false, 0)
	assertErrIs(t, err, ErrInvalid)
}

func TestRequeueKeepsBitsetDeadline(t *testing.T) {
	f := New(100)
	mustWait(t, f, 1, 1, 0, 0x1, 10, 100)
	mustWait(t, f, 2, 1, 0, 0x2, 10, 50)
	_, rq, err := f.Requeue(1, 2, 0, 2, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "moved", rq, []int{1, 2})
	if st, addr, _ := f.StateOf(1); st != StateWaiting || addr != 2 {
		t.Fatalf("tid1 state=%v addr=%d", st, addr)
	}
	w, err := f.Wake(2, 10, 0x1)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "bitset wake after requeue", w, []int{1})
	ex, err := f.Advance(60)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "timeout after requeue", ex, []int{2})
}

func TestWakeOp(t *testing.T) {
	f := New(100)
	f.Store(100, 5)
	mustWait(t, f, 1, 200, 0, 1, 0, 0)
	mustWait(t, f, 2, 200, 0, 1, 0, 0)
	mustWait(t, f, 3, 100, 5, 1, 0, 0)

	// old=5, new=6; EQ 5 compares against the OLD value -> true.
	w1, w2, err := f.WakeOp(200, 100, 1, 1, OpAdd, 1, CmpEq, 5)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "w1", w1, []int{1})
	eqInts(t, "w2", w2, []int{3})
	if f.Load(100) != 6 {
		t.Fatalf("mem=%d want 6", f.Load(100))
	}

	// old=6, new=7; EQ 7 differs between old (false) and new (true).
	mustWait(t, f, 4, 200, 0, 1, 0, 0)
	mustWait(t, f, 5, 100, 6, 1, 0, 0)
	w1, w2, err = f.WakeOp(200, 100, 1, 1, OpAdd, 1, CmpEq, 7)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "w1", w1, []int{2})
	eqInts(t, "w2 empty", w2, nil)
	if f.Load(100) != 7 {
		t.Fatalf("mem=%d want 7 (mutation happens regardless of cmp)", f.Load(100))
	}
	eqInts(t, "100 waiter kept", f.Waiters(100), []int{5})

	// a1 == a2: n1 then n2 over the remainder, no duplicate.
	f.Store(300, 0)
	mustWait(t, f, 6, 300, 0, 1, 0, 0)
	mustWait(t, f, 7, 300, 0, 1, 0, 0)
	mustWait(t, f, 8, 300, 0, 1, 0, 0)
	w1, w2, err = f.WakeOp(300, 300, 2, 2, OpSet, 1, CmpEq, 0)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "same-addr w1", w1, []int{6, 7})
	eqInts(t, "same-addr w2", w2, []int{8})

	_, _, err = f.WakeOp(0, 0, -1, 0, OpSet, 0, CmpEq, 0)
	assertErrIs(t, err, ErrInvalid)
	_, _, err = f.WakeOp(0, 0, 0, 0, Op(99), 0, CmpEq, 0)
	assertErrIs(t, err, ErrInvalid)
	_, _, err = f.WakeOp(0, 0, 0, 0, OpSet, 0, Cmp(99), 0)
	assertErrIs(t, err, ErrInvalid)
}

func TestWakeOpOps(t *testing.T) {
	cases := []struct {
		op         Op
		old, arg   int32
		want       int32
		cmp        Cmp
		cmpArg     int32
		wantSecond bool
	}{
		{OpSet, 10, 7, 7, CmpEq, 10, true},
		{OpAdd, 1 << 30, 1 << 30, -1 << 31, CmpGt, 0, true}, // int32 wrap
		{OpOr, 0b0011, 0b1010, 0b1011, CmpLt, 0, false},
		{OpAndn, 0b1111, 0b0101, 0b1010, CmpNe, 0b1111, false},
		{OpXor, 0b1100, 0b0110, 0b1010, CmpLe, 0b1111, true},
	}
	for i, c := range cases {
		f := New(100)
		f.Store(100, c.old)
		mustWait(t, f, 100+i*2, 200, 0, 1, 0, 0)
		mustWait(t, f, 101+i*2, 100, c.old, 1, 0, 0)
		w1, w2, err := f.WakeOp(200, 100, 1, 1, c.op, c.arg, c.cmp, c.cmpArg)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := f.Load(100); got != c.want {
			t.Fatalf("case %d: mem=%d want %d", i, got, c.want)
		}
		if len(w1) != 1 {
			t.Fatalf("case %d: w1=%v", i, w1)
		}
		if (len(w2) == 1) != c.wantSecond {
			t.Fatalf("case %d: w2=%v wantSecond=%v", i, w2, c.wantSecond)
		}
	}
}
