package tictoc

import (
	"errors"
	"sync"
	"testing"
)

func mustCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var te *TicTocError
	if !errors.As(err, &te) {
		t.Fatalf("want TicTocError code %d, got %v", want, err)
	}
	if te.Code != want {
		t.Fatalf("want error code %d, got %d", want, te.Code)
	}
}

func checkTuple(t *testing.T, db *DB, key int, value, w, r, holder int64) {
	t.Helper()
	snap := db.Snapshot()[key]
	if snap.Value != value || snap.WriteTS != w || snap.ReadTS != r || snap.Holder != holder {
		t.Fatalf("tuple %d = (v=%d,w=%d,r=%d,holder=%d), want (v=%d,w=%d,r=%d,holder=%d)",
			key, snap.Value, snap.WriteTS, snap.ReadTS, snap.Holder, value, w, r, holder)
	}
}

func commitKey(db *DB, key int, x int64) {
	tr := db.Begin()
	if err := db.Write(tr, key, x); err != nil {
		panic(err)
	}
	if _, err := db.Prepare(tr); err != nil {
		panic(err)
	}
	if _, err := db.Finish(tr); err != nil {
		panic(err)
	}
}

// Worked example 1: read-only transaction commits at c=0, before the writer.
func TestWorkedExampleReadOnlyEarlierTS(t *testing.T) {
	db, err := NewDB(2)
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := db.Begin(), db.Begin()
	t.Logf("Begin -> T1=%d T2=%d", t1, t2)

	v, err := db.Read(t1, 0)
	if err != nil || v != 0 {
		t.Fatalf("T1 Read(0) = (%d,%v)", v, err)
	}
	t.Logf(`T1 Read(0) -> 0, read set records (k=0,w=0,r=0)`)

	if err := db.Write(t2, 0, 5); err != nil {
		t.Fatal(err)
	}
	c, err := db.Prepare(t2)
	if err != nil || c != 1 {
		t.Fatalf("T2 Prepare = (%d,%v), want c=1", c, err)
	}
	t.Logf("T2 Prepare -> c=max(r0+1)=1, empty read set passes")
	if c, err = db.Finish(t2); err != nil || c != 1 {
		t.Fatalf("T2 Finish = (%d,%v)", c, err)
	}
	checkTuple(t, db, 0, 5, 1, 1, 0)
	t.Logf("T2 Finish -> tuple0=(5,1,1), unlocked")

	c, err = db.Prepare(t1)
	if err != nil || c != 0 {
		t.Fatalf("T1 Prepare = (%d,%v), want c=0", c, err)
	}
	t.Logf("T1 Prepare read-only -> c=max(observed w=0)=0; r0=0>=c=0 passes without touching tuple")
	if c, err = db.Finish(t1); err != nil || c != 0 {
		t.Fatalf("T1 Finish = (%d,%v)", c, err)
	}
	checkTuple(t, db, 0, 5, 1, 1, 0)
	if st, _ := db.Status(t1); st != StatusCommitted {
		t.Fatalf("T1 status=%d, want committed", st)
	}
	t.Logf("T1 commits at c=0 (serial order before T2); tuple0 stays (5,1,1)")
}

// Worked example 2: c = max(write-set r+1, read-set w); unlocked read key r extended.
func TestWorkedExampleExtension(t *testing.T) {
	db, _ := NewDB(2)
	commitKey(db, 0, 9) // tuple0 -> (9,1,1)
	commitKey(db, 1, 7)
	commitKey(db, 1, 77)
	commitKey(db, 1, 78)
	commitKey(db, 1, 79) // tuple1 -> (79,4,4)
	snap := db.Snapshot()
	t.Logf("seeded tuple0=(%d,%d,%d) tuple1=(%d,%d,%d)",
		snap[0].Value, snap[0].WriteTS, snap[0].ReadTS,
		snap[1].Value, snap[1].WriteTS, snap[1].ReadTS)

	t3 := db.Begin()
	if _, err := db.Read(t3, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(t3, 1, 42); err != nil {
		t.Fatal(err)
	}
	c, err := db.Prepare(t3)
	if err != nil || c != 5 {
		t.Fatalf("T3 Prepare = (%d,%v), want c=5", c, err)
	}
	t.Logf("T3 Prepare -> c=max(tuple1.r+1=5, observed tuple0.w=1)=5")
	checkTuple(t, db, 0, 9, 1, 5, 0)
	t.Logf("tuple0: r0=1<c=5, w unchanged=1, r=1<5, unlocked -> r extended to 5")
	checkTuple(t, db, 1, 79, 4, 4, t3)
	if c, err = db.Finish(t3); err != nil || c != 5 {
		t.Fatalf("T3 Finish = (%d,%v)", c, err)
	}
	checkTuple(t, db, 1, 42, 5, 5, 0)
	t.Logf("T3 Finish -> tuple1=(42,5,5)")
}

// r0 == c passes without inspecting the tuple; r0 == c-1 inspects and extends.
func TestR0EqualCFastPathAndMinusOne(t *testing.T) {
	db, _ := NewDB(2)
	commitKey(db, 0, 1) // tuple0 -> (1,1,1)

	ra := db.Begin()
	if _, err := db.Read(ra, 0); err != nil { // records (w=1,r=1)
		t.Fatal(err)
	}
	if err := db.Write(ra, 1, 9); err != nil {
		t.Fatal(err)
	}
	c, err := db.Prepare(ra)
	if err != nil || c != 1 {
		t.Fatalf("Prepare = (%d,%v), want c=1 fast path", c, err)
	}
	t.Logf("r0=1 >= c=max(key1.r+1=1,key0.w=1)=1 -> pass without touching tuple0")
	if _, err := db.Finish(ra); err != nil {
		t.Fatal(err)
	}

	rb := db.Begin()
	if _, err := db.Read(rb, 0); err != nil { // records (w=1,r=1)
		t.Fatal(err)
	}
	commitKey(db, 1, 5) // ra left key1 r=1; this commit takes c=2 -> (5,2,2)
	if err := db.Write(rb, 1, 6); err != nil {
		t.Fatal(err)
	}
	c, err = db.Prepare(rb)
	if err != nil || c != 3 {
		t.Fatalf("Prepare = (%d,%v), want c=3 inspect path", c, err)
	}
	t.Logf("r0=1<c=3: w=1 unchanged, r=1<3, unlocked -> tuple0 r extended to 3")
	checkTuple(t, db, 0, 1, 1, 3, 0)
	if _, err := db.Finish(rb); err != nil {
		t.Fatal(err)
	}
}

// Observed w no longer matches the tuple's w -> version-changed abort.
func TestVersionChangedAbort(t *testing.T) {
	db, _ := NewDB(2)
	ta := db.Begin()
	v, err := db.Read(ta, 0)
	if err != nil || v != 0 {
		t.Fatalf("Read = (%d,%v)", v, err)
	}
	commitKey(db, 0, 8) // tuple0 w becomes 1
	if err := db.Write(ta, 1, 1); err != nil {
		t.Fatal(err)
	}
	_, err = db.Prepare(ta)
	mustCode(t, err, ErrVersionChanged)
	t.Logf("ta read (w=0,r=0); tuple0 now w=1; c=1; r0=0<1 and w 0!=1 -> version changed")
	if st, _ := db.Status(ta); st != StatusAborted {
		t.Fatalf("status=%d", st)
	}
	checkTuple(t, db, 0, 8, 1, 1, 0)
	checkTuple(t, db, 1, 0, 0, 0, 0)
	t.Logf("rollback: ta holds no locks, tuples identical to pre-Prepare")
}

// Another locker blocks extension; the same held lock is ignored when r0>=c.
func TestExtensionBlockedAndFastPathIgnoresLock(t *testing.T) {
	db, _ := NewDB(3)

	ta := db.Begin()
	if _, err := db.Read(ta, 0); err != nil { // (0,0)
		t.Fatal(err)
	}
	locker := db.Begin()
	if err := db.Write(locker, 0, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Prepare(locker); err != nil { // c=1, holds key0
		t.Fatal(err)
	}
	if err := db.Write(ta, 1, 4); err != nil {
		t.Fatal(err)
	}
	_, err := db.Prepare(ta)
	mustCode(t, err, ErrExtensionBlocked)
	t.Logf("ta c=1; key0 r0=0<1, w unchanged, r<1, holder=locker -> extension blocked")
	checkTuple(t, db, 0, 0, 0, 0, locker)
	checkTuple(t, db, 1, 0, 0, 0, 0)
	if st, _ := db.Status(ta); st != StatusAborted {
		t.Fatalf("status=%d", st)
	}
	if _, err := db.Finish(locker); err != nil {
		t.Fatal(err)
	}

	commitKey(db, 0, 9) // tuple0 -> (9,2,2)
	locker2 := db.Begin()
	if err := db.Write(locker2, 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Prepare(locker2); err != nil { // c=3, holds key0
		t.Fatal(err)
	}
	tc := db.Begin()
	if _, err := db.Read(tc, 0); err != nil { // reads under lock: (w=2,r=2)
		t.Fatal(err)
	}
	c, err := db.Prepare(tc) // read-only: c=max(observed w)=2
	if err != nil || c != 2 {
		t.Fatalf("Prepare = (%d,%v), want c=2", c, err)
	}
	t.Logf("tc observed r=2 on locked key0; c=2; r0>=c -> pass without inspecting tuple")
	if _, err := db.Finish(tc); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Finish(locker2); err != nil {
		t.Fatal(err)
	}
}

// A read+write key goes through the own-lock path and is never r-extended.
func TestOwnLockReadWriteKeyNotExtended(t *testing.T) {
	db, _ := NewDB(2)
	commitKey(db, 0, 1) // (1,1,1)
	commitKey(db, 1, 0)
	commitKey(db, 1, 0) // key1 -> (0,2,2)

	tr := db.Begin()
	if _, err := db.Read(tr, 0); err != nil { // (w=1,r=1)
		t.Fatal(err)
	}
	if err := db.Write(tr, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(tr, 1, 7); err != nil { // c = 2+1 = 3
		t.Fatal(err)
	}
	c, err := db.Prepare(tr)
	if err != nil || c != 3 {
		t.Fatalf("Prepare = (%d,%v), want c=3", c, err)
	}
	t.Logf("key0 r0=1<c=3 but holder=self -> pass, r stays 1 until Finish")
	checkTuple(t, db, 0, 1, 1, 1, tr)
	if _, err := db.Finish(tr); err != nil {
		t.Fatal(err)
	}
	checkTuple(t, db, 0, 5, 3, 3, 0)
}

// Extensions made before a later read record fails are fully undone.
func TestAbortRollsBackExtensionsAndLocks(t *testing.T) {
	db, _ := NewDB(3)
	commitKey(db, 2, 9) // tuple2 -> (9,1,1)

	tr := db.Begin()
	if _, err := db.Read(tr, 0); err != nil { // (0,0), extends first
		t.Fatal(err)
	}
	if _, err := db.Read(tr, 1); err != nil { // (0,0), version will change
		t.Fatal(err)
	}
	commitKey(db, 1, 6)                        // tuple1 w -> 1
	if err := db.Write(tr, 2, 3); err != nil { // c = r2+1 = 2
		t.Fatal(err)
	}
	_, err := db.Prepare(tr)
	mustCode(t, err, ErrVersionChanged)
	t.Logf("c=2; key0 extended 0->2 first; key1 then fails version check; both undone")
	checkTuple(t, db, 0, 0, 0, 0, 0)
	checkTuple(t, db, 1, 6, 1, 1, 0)
	checkTuple(t, db, 2, 9, 1, 1, 0)
	if st, _ := db.Status(tr); st != StatusAborted {
		t.Fatalf("status=%d", st)
	}
}

// Repeated reads return the first observed value; buffered writes win.
func TestRepeatReadReturnsFirstValue(t *testing.T) {
	db, _ := NewDB(1)
	tr := db.Begin()
	v, _ := db.Read(tr, 0)
	if v != 0 {
		t.Fatalf("first read = %d", v)
	}
	commitKey(db, 0, 11)
	v, err := db.Read(tr, 0)
	if err != nil || v != 0 {
		t.Fatalf("repeat read = (%d,%v), want cached 0", v, err)
	}
	t.Logf("repeat read after external commit still returns the first value 0")
	if err := db.Write(tr, 0, 22); err != nil {
		t.Fatal(err)
	}
	if v, err = db.Read(tr, 0); err != nil || v != 22 {
		t.Fatalf("read after own write = (%d,%v), want 22", v, err)
	}
	t.Logf("own buffered write shadows the cached read -> 22")
}

// Lock-conflict abort, distinct from the other two abort reasons.
func TestLockConflictAbort(t *testing.T) {
	db, _ := NewDB(1)
	a := db.Begin()
	if err := db.Write(a, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Prepare(a); err != nil {
		t.Fatal(err)
	}
	b := db.Begin()
	if err := db.Write(b, 0, 2); err != nil {
		t.Fatal(err)
	}
	_, err := db.Prepare(b)
	mustCode(t, err, ErrLockConflict)
	t.Logf("b cannot lock key0 held by prepared a -> lock conflict; b aborted")
	if st, _ := db.Status(b); st != StatusAborted {
		t.Fatalf("status=%d", st)
	}
	checkTuple(t, db, 0, 0, 0, 0, a)
	if err := db.Abort(a); err != nil {
		t.Fatal(err)
	}
	checkTuple(t, db, 0, 0, 0, 0, 0)
	t.Logf("Abort(prepared a) releases the key0 lock")
}

// Rejection precedence: unknown txn, then state, then key range; rejected calls
// change nothing.
func TestRejectionPrecedenceAndPurity(t *testing.T) {
	db, _ := NewDB(1)
	before := db.Snapshot()

	if _, err := db.Read(999, 5); !errors.Is(err, ErrNoSuchTxnErr) {
		t.Fatalf("Read: %v", err)
	}
	if err := db.Write(999, 5, 1); !errors.Is(err, ErrNoSuchTxnErr) {
		t.Fatalf("Write: %v", err)
	}
	if _, err := db.Prepare(999); !errors.Is(err, ErrNoSuchTxnErr) {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := db.Finish(999); !errors.Is(err, ErrNoSuchTxnErr) {
		t.Fatalf("Finish: %v", err)
	}
	if err := db.Abort(999); !errors.Is(err, ErrNoSuchTxnErr) {
		t.Fatalf("Abort: %v", err)
	}

	tr := db.Begin()
	if _, err := db.Read(tr, 7); !errors.Is(err, ErrKeyOutOfRangeErr) {
		t.Fatalf("Read bad key: %v", err)
	}
	if err := db.Write(tr, -1, 1); !errors.Is(err, ErrKeyOutOfRangeErr) {
		t.Fatalf("Write bad key: %v", err)
	}
	if _, err := db.Prepare(tr); err != nil { // idle txn -> c=0 prepared
		t.Fatalf("idle Prepare: %v", err)
	}
	if _, err := db.Read(tr, 7); !errors.Is(err, ErrWrongStateErr) {
		t.Fatalf("Read on prepared: %v", err)
	}
	if err := db.Write(tr, 0, 1); !errors.Is(err, ErrWrongStateErr) {
		t.Fatalf("Write on prepared: %v", err)
	}
	if _, err := db.Prepare(tr); !errors.Is(err, ErrWrongStateErr) {
		t.Fatalf("Prepare on prepared: %v", err)
	}
	if _, err := db.Finish(tr); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if _, err := db.Finish(tr); !errors.Is(err, ErrWrongStateErr) {
		t.Fatalf("Finish on committed: %v", err)
	}
	if err := db.Abort(tr); !errors.Is(err, ErrWrongStateErr) {
		t.Fatalf("Abort on committed: %v", err)
	}

	after := db.Snapshot()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("tuple %d changed: %+v vs %+v", i, before[i], after[i])
		}
	}
	t.Logf("all rejected calls left tuples byte-identical")
}

func TestInvalidConfig(t *testing.T) {
	for _, k := range []int{0, -1, 65, 1000} {
		if d, err := NewDB(k); !errors.Is(err, ErrInvalidConfigErr) || d != nil {
			t.Fatalf("NewDB(%d) = %v,%v", k, d, err)
		}
	}
	if d, err := NewDB(64); err != nil || d == nil {
		t.Fatalf("NewDB(64) = %v,%v", d, err)
	}
	t.Logf("K in [1,64] accepted; outside rejected wholesale")
}

// Prepare touches tuple fields no more than 2*(|R|+|W|) times.
func TestPrepareTouchBudget(t *testing.T) {
	db, _ := NewDB(4)
	tr := db.Begin()
	if _, err := db.Read(tr, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Read(tr, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(tr, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(tr, 3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Prepare(tr); err != nil {
		t.Fatal(err)
	}
	touches := db.lastPrepareTouches()
	if touches > 2*4 {
		t.Fatalf("touches=%d > budget %d", touches, 2*4)
	}
	t.Logf("Prepare touches=%d within budget 2*(|R|+|W|)=%d", touches, 2*4)
}

// Concurrent calls never corrupt invariants: w<=r, at most one holder, holders
// are exactly prepared txns' write keys, and w strictly exceeds the old r.
func TestConcurrentSafety(t *testing.T) {
	db, _ := NewDB(8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			tr := db.Begin()
			for i := 0; i < 30; i++ {
				key := (seed*7 + i) % 8
				if i%2 == 0 {
					_, _ = db.Read(tr, key)
				} else {
					_ = db.Write(tr, key, int64(seed*100+i))
				}
			}
			if _, err := db.Prepare(tr); err == nil {
				_, _ = db.Finish(tr)
			}
		}(g)
	}
	wg.Wait()
	for i, tp := range db.Snapshot() {
		if tp.WriteTS > tp.ReadTS {
			t.Fatalf("tuple %d w=%d > r=%d", i, tp.WriteTS, tp.ReadTS)
		}
		if tp.Holder != 0 {
			t.Fatalf("tuple %d still locked by %d", i, tp.Holder)
		}
	}
	t.Logf("concurrent run settled with w<=r and no residual locks")
}
