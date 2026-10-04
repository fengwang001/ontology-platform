package tictoc

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, k int) *Validator {
	t.Helper()
	v, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	return v
}

func mustRead(t *testing.T, v *Validator, txn, k int) int64 {
	t.Helper()
	x, err := v.Read(txn, k)
	if err != nil {
		t.Fatalf("Read(%d,%d): %v", txn, k, err)
	}
	return x
}

func mustWrite(t *testing.T, v *Validator, txn, k int, x int64) {
	t.Helper()
	if err := v.Write(txn, k, x); err != nil {
		t.Fatalf("Write(%d,%d,%d): %v", txn, k, x, err)
	}
}

func mustPrepare(t *testing.T, v *Validator, txn int) int64 {
	t.Helper()
	c, err := v.Prepare(txn)
	if err != nil {
		t.Fatalf("Prepare(%d): %v", txn, err)
	}
	return c
}

func mustFinish(t *testing.T, v *Validator, txn int) int64 {
	t.Helper()
	c, err := v.Finish(txn)
	if err != nil {
		t.Fatalf("Finish(%d): %v", txn, err)
	}
	return c
}

func wantErr(t *testing.T, err, sentinel error, ctx string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("%s: got error %v, want %v", ctx, err, sentinel)
	}
}

func tuplesOf(v *Validator) []Tuple {
	tuples, _ := v.Snapshot()
	return tuples
}

// setTuple directly seeds a tuple (white-box test helper).
func setTuple(v *Validator, k int, value, w, r int64) {
	v.tuples[k] = Tuple{Value: value, W: w, R: r}
}

// Spec example 1: a read-only transaction commits with a smaller
// timestamp than a writer that committed earlier in real time.
func TestReadOnlyCommitsWithSmallerTimestamp(t *testing.T) {
	v := mustNew(t, 2)
	t1, t2 := v.Begin(), v.Begin()

	if got := mustRead(t, v, t1, 0); got != 0 {
		t.Fatalf("T1 read tuple0 = %d, want 0", got)
	}
	mustWrite(t, v, t2, 0, 5)
	if c := mustPrepare(t, v, t2); c != 1 {
		t.Fatalf("T2 commit ts = %d, want 1 (r+1)", c)
	}
	mustFinish(t, v, t2)
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 5, W: 1, R: 1}) {
		t.Fatalf("tuple0 after T2 finish = %+v, want {5 1 1}", tp)
	}

	// T1 is read-only: c = max(read w) = 0, and r0 = 0 >= c passes
	// without touching the tuple, so T1 commits at ts 0, ordered
	// before T2.
	if c := mustPrepare(t, v, t1); c != 0 {
		t.Fatalf("T1 commit ts = %d, want 0", c)
	}
	if c := mustFinish(t, v, t1); c != 0 {
		t.Fatalf("T1 finish ts = %d, want 0", c)
	}
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 5, W: 1, R: 1}) {
		t.Fatalf("tuple0 after T1 finish = %+v, want unchanged {5 1 1}", tp)
	}
}

// Spec example 2: c = max(write-set r+1, read-set w) and the read
// version is extended when needed.
func TestCommitTimestampMaxAndExtension(t *testing.T) {
	v := mustNew(t, 2)
	setTuple(v, 0, 9, 1, 1) // (x, 1, 1)
	setTuple(v, 1, 7, 4, 4) // (x, 4, 4)

	txn := v.Begin()
	if got := mustRead(t, v, txn, 0); got != 9 {
		t.Fatalf("read tuple0 = %d, want 9", got)
	}
	mustWrite(t, v, txn, 1, 42)
	// c = max(r1+1, w0) = max(4+1, 1) = 5.
	if c := mustPrepare(t, v, txn); c != 5 {
		t.Fatalf("commit ts = %d, want 5", c)
	}
	// tuple0: r0=1 < 5, w unchanged, r < c, unlocked => r extended to 5.
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 9, W: 1, R: 5}) {
		t.Fatalf("tuple0 after prepare = %+v, want {9 1 5}", tp)
	}
	if c := mustFinish(t, v, txn); c != 5 {
		t.Fatalf("finish ts = %d, want 5", c)
	}
	// Finish installs w strictly greater than the old r (4).
	if tp := tuplesOf(v)[1]; tp != (Tuple{Value: 42, W: 5, R: 5}) {
		t.Fatalf("tuple1 after finish = %+v, want {42 5 5}", tp)
	}
}

// r0 == c passes without even looking at the tuple (a changed w does
// not matter); r0 == c-1 forces the version check.
func TestR0EqualCSkipsTuple(t *testing.T) {
	// Case A: r0 == c, tuple's w changed meanwhile => still passes.
	v := mustNew(t, 2)
	t1 := v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0)
	// Someone else overwrites tuple0 and commits at ts 1.
	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 5)
	mustPrepare(t, v, t2)
	mustFinish(t, v, t2)
	// T1 is read-only: c = w0 = 0, r0 = 0 >= c => pass without
	// checking tuple0's w (which is now 1 != w0).
	if c := mustPrepare(t, v, t1); c != 0 {
		t.Fatalf("case A commit ts = %d, want 0", c)
	}

	// Case B: r0 == c-1, same changed w => version-change abort.
	v = mustNew(t, 2)
	t1 = v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0)
	t2 = v.Begin()
	mustWrite(t, v, t2, 0, 5)
	mustPrepare(t, v, t2)
	mustFinish(t, v, t2)
	// A write to tuple1 (r=0) lifts c to r+1 = 1 = r0+1, so tuple0's
	// record no longer short-circuits and the version check fires.
	mustWrite(t, v, t1, 1, 11)
	_, err := v.Prepare(t1)
	wantErr(t, err, ErrAbortVersionChanged, "case B Prepare")
	if _, states := v.Snapshot(); states[t1] != Aborted {
		t.Fatalf("case B: T1 state = %s, want aborted", states[t1])
	}
}

// A read version that someone else overwrote aborts the prepare.
func TestAbortVersionChanged(t *testing.T) {
	v := mustNew(t, 3)
	setTuple(v, 2, 0, 0, 4) // write target with r=4 to force c=5

	t1 := v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0)

	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 5)
	mustPrepare(t, v, t2)
	mustFinish(t, v, t2) // tuple0 now (5,1,1)

	mustWrite(t, v, t1, 2, 99) // c = max(4+1, 0) = 5 > r0
	before := tuplesOf(v)
	_, err := v.Prepare(t1)
	wantErr(t, err, ErrAbortVersionChanged, "Prepare")
	if after := tuplesOf(v); !reflect.DeepEqual(before, after) {
		t.Fatalf("tuples changed by aborted prepare:\nbefore=%v\nafter =%v", before, after)
	}
	if _, states := v.Snapshot(); states[t1] != Aborted {
		t.Fatalf("T1 state = %s, want aborted", states[t1])
	}
	// An aborted transaction rejects every further call.
	if _, err := v.Read(t1, 0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Read on aborted txn: %v", err)
	}
	if err := v.Write(t1, 0, 1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Write on aborted txn: %v", err)
	}
	if _, err := v.Prepare(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Prepare on aborted txn: %v", err)
	}
	if _, err := v.Finish(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Finish on aborted txn: %v", err)
	}
	if err := v.Abort(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Abort on aborted txn: %v", err)
	}
}

// A needed extension blocked by a foreign lock aborts; when r0 >= c
// the same foreign lock does not matter.
func TestAbortExtendBlockedAndR0CoversLock(t *testing.T) {
	// Case A: extension needed but tuple locked by someone else.
	v := mustNew(t, 2)
	setTuple(v, 1, 0, 0, 3) // write target with r=3 to force c=4

	t1 := v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0)
	mustWrite(t, v, t1, 1, 8)

	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 5)
	if c := mustPrepare(t, v, t2); c != 1 {
		t.Fatalf("T2 commit ts = %d, want 1", c)
	}
	// T2 is prepared and holds the lock on tuple0 (w still 0).

	before := tuplesOf(v)
	_, err := v.Prepare(t1) // c=4, tuple0 needs extension, locked by T2
	wantErr(t, err, ErrAbortExtendBlocked, "Prepare")
	if after := tuplesOf(v); !reflect.DeepEqual(before, after) {
		t.Fatalf("tuples changed by aborted prepare:\nbefore=%v\nafter =%v", before, after)
	}

	// Case B: r0 >= c, so the foreign lock is never consulted.
	v = mustNew(t, 1)
	t1 = v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0); read-only => c=0
	t2 = v.Begin()
	mustWrite(t, v, t2, 0, 5)
	mustPrepare(t, v, t2) // T2 holds the lock on tuple0
	if c := mustPrepare(t, v, t1); c != 0 {
		t.Fatalf("case B commit ts = %d, want 0", c)
	}
}

// A tuple that is both read and written by t is locked by t itself
// during Prepare and passes without extending r.
func TestOwnLockReadWriteKeyNotExtended(t *testing.T) {
	v := mustNew(t, 2)
	setTuple(v, 1, 0, 0, 4) // write target with r=4 to force c=5

	t1 := v.Begin()
	mustRead(t, v, t1, 0) // records (w0=0, r0=0)
	mustWrite(t, v, t1, 0, 7)
	mustWrite(t, v, t1, 1, 9)

	if c := mustPrepare(t, v, t1); c != 5 {
		t.Fatalf("commit ts = %d, want 5", c)
	}
	// tuple0 needed an extension (r0=0 < 5) but t1 holds its lock, so
	// r stays 0 until Finish.
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 0, W: 0, R: 0, Lock: t1}) {
		t.Fatalf("tuple0 after prepare = %+v, want {0 0 0 lock=t1}", tp)
	}
	mustFinish(t, v, t1)
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 7, W: 5, R: 5}) {
		t.Fatalf("tuple0 after finish = %+v, want {7 5 5}", tp)
	}
}

// An abort rolls back read-version extensions made earlier in the
// same Prepare call.
func TestAbortRollsBackExtension(t *testing.T) {
	v := mustNew(t, 3)
	setTuple(v, 2, 0, 0, 2) // write target with r=2 to force c=3

	t1 := v.Begin()
	mustRead(t, v, t1, 0) // (w0=0, r0=0): will be extended to 3
	mustRead(t, v, t1, 1) // (w0=0, r0=0): version will have changed
	mustWrite(t, v, t1, 2, 9)

	t2 := v.Begin()
	mustWrite(t, v, t2, 1, 5)
	mustPrepare(t, v, t2)
	mustFinish(t, v, t2) // tuple1 now (5,1,1)

	before := tuplesOf(v)
	_, err := v.Prepare(t1)
	wantErr(t, err, ErrAbortVersionChanged, "Prepare")
	after := tuplesOf(v)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("extension not rolled back:\nbefore=%v\nafter =%v", before, after)
	}
	if after[0].R != 0 {
		t.Fatalf("tuple0 r = %d after abort, want 0 (extension undone)", after[0].R)
	}
	if after[2].Lock != 0 {
		t.Fatalf("tuple2 lock = %d after abort, want 0 (lock released)", after[2].Lock)
	}
}

// A repeated read returns the value of the first read and does not
// refresh the read-set record.
func TestRepeatReadReturnsFirstValue(t *testing.T) {
	v := mustNew(t, 1)
	t1 := v.Begin()
	if got := mustRead(t, v, t1, 0); got != 0 {
		t.Fatalf("first read = %d, want 0", got)
	}

	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 7)
	mustPrepare(t, v, t2)
	mustFinish(t, v, t2) // tuple0 now (7,1,1)

	if got := mustRead(t, v, t1, 0); got != 0 {
		t.Fatalf("repeat read = %d, want first-read value 0", got)
	}
	// The record still holds the original (w0=0, r0=0), so a read-only
	// prepare commits at c=0.
	if c := mustPrepare(t, v, t1); c != 0 {
		t.Fatalf("commit ts = %d, want 0", c)
	}
}

// Write-set lock conflict with another prepared transaction.
func TestAbortLockConflict(t *testing.T) {
	v := mustNew(t, 2)
	t1 := v.Begin()
	mustWrite(t, v, t1, 0, 1)
	mustPrepare(t, v, t1) // t1 holds the lock on tuple0

	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 2)
	before := tuplesOf(v)
	_, err := v.Prepare(t2)
	wantErr(t, err, ErrAbortLockConflict, "Prepare")
	if after := tuplesOf(v); !reflect.DeepEqual(before, after) {
		t.Fatalf("tuples changed by aborted prepare:\nbefore=%v\nafter =%v", before, after)
	}
	if _, states := v.Snapshot(); states[t2] != Aborted {
		t.Fatalf("T2 state = %s, want aborted", states[t2])
	}
	// The lock holder is unaffected and can still finish.
	if c := mustFinish(t, v, t1); c != 1 {
		t.Fatalf("T1 finish ts = %d, want 1", c)
	}
	if tp := tuplesOf(v)[0]; tp != (Tuple{Value: 1, W: 1, R: 1}) {
		t.Fatalf("tuple0 = %+v, want {1 1 1}", tp)
	}
}

// Rejection reasons are distinguishable and reported in the required
// order; rejected calls change nothing.
func TestRejections(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(0): %v", err)
	}
	if _, err := New(65); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(65): %v", err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(-3): %v", err)
	}

	v := mustNew(t, 2)
	txn := v.Begin()
	before := tuplesOf(v)

	// Unknown transaction id beats every other reason.
	if _, err := v.Read(999, 99); !errors.Is(err, ErrTxnNotFound) {
		t.Fatalf("Read unknown txn: %v", err)
	}
	if err := v.Write(999, 99, 1); !errors.Is(err, ErrTxnNotFound) {
		t.Fatalf("Write unknown txn: %v", err)
	}
	if _, err := v.Prepare(999); !errors.Is(err, ErrTxnNotFound) {
		t.Fatalf("Prepare unknown txn: %v", err)
	}
	if _, err := v.Finish(999); !errors.Is(err, ErrTxnNotFound) {
		t.Fatalf("Finish unknown txn: %v", err)
	}
	if err := v.Abort(999); !errors.Is(err, ErrTxnNotFound) {
		t.Fatalf("Abort unknown txn: %v", err)
	}

	// State mismatch beats tuple-index range.
	if _, err := v.Finish(txn); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Finish on active txn: %v", err)
	}
	if err := v.Abort(txn); err != nil {
		t.Fatalf("Abort on active txn: %v", err)
	}
	if _, err := v.Read(txn, 1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Read on aborted txn beats range check: %v", err)
	}

	// Tuple index range is checked for Read/Write on active txns.
	txn2 := v.Begin()
	if _, err := v.Read(txn2, -1); !errors.Is(err, ErrTupleOutOfRange) {
		t.Fatalf("Read k=-1: %v", err)
	}
	if _, err := v.Read(txn2, 2); !errors.Is(err, ErrTupleOutOfRange) {
		t.Fatalf("Read k=K: %v", err)
	}
	if err := v.Write(txn2, 2, 1); !errors.Is(err, ErrTupleOutOfRange) {
		t.Fatalf("Write k=K: %v", err)
	}

	// A committed transaction rejects every further call.
	txn3 := v.Begin()
	mustPrepare(t, v, txn3)
	mustFinish(t, v, txn3)
	if _, err := v.Read(txn3, 0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Read on committed txn: %v", err)
	}
	if err := v.Abort(txn3); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Abort on committed txn: %v", err)
	}

	if after := tuplesOf(v); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected calls changed tuples:\nbefore=%v\nafter =%v", before, after)
	}
}

// Abort releases the locks of a prepared transaction.
func TestAbortReleasesLocks(t *testing.T) {
	v := mustNew(t, 2)
	t1 := v.Begin()
	mustWrite(t, v, t1, 0, 1)
	mustWrite(t, v, t1, 1, 2)
	mustPrepare(t, v, t1)
	if err := v.Abort(t1); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	for k, tp := range tuplesOf(v) {
		if tp.Lock != 0 {
			t.Fatalf("tuple%d still locked by %d after abort", k, tp.Lock)
		}
	}
	// The freed tuples can be locked by someone else now.
	t2 := v.Begin()
	mustWrite(t, v, t2, 0, 3)
	mustPrepare(t, v, t2)
}

// An empty transaction (no reads, no writes) commits with c = 0.
func TestEmptyTransactionCommitsAtZero(t *testing.T) {
	v := mustNew(t, 1)
	txn := v.Begin()
	if c := mustPrepare(t, v, txn); c != 0 {
		t.Fatalf("commit ts = %d, want 0", c)
	}
	if c := mustFinish(t, v, txn); c != 0 {
		t.Fatalf("finish ts = %d, want 0", c)
	}
}
