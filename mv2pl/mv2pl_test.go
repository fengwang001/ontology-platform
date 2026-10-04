package mv2pl

import (
	"reflect"
	"testing"
)

func grantEvent(txn, key int, mode Mode, value int64) Event {
	return Event{Kind: EventGrant, Txn: txn, Key: key, Mode: mode, Value: value}
}

func commitEvent(txn int, writes ...WriteEntry) Event {
	return Event{Kind: EventCommit, Txn: txn, Writes: writes}
}

func mustNew(t *testing.T, k int) *Manager {
	t.Helper()
	m, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	return m
}

func expectEvents(t *testing.T, what string, res Result, want []Event) {
	t.Helper()
	if !res.OK {
		t.Fatalf("%s: unexpectedly rejected: %s", what, res.Reject)
	}
	if len(res.Events) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(res.Events, want) {
		t.Fatalf("%s: events = %v, want %v", what, res.Events, want)
	}
}

func expectReject(t *testing.T, what string, res Result, reason RejectReason) {
	t.Helper()
	if res.OK || res.Reject != reason {
		t.Fatalf("%s: got (ok=%v, reject=%s), want reject %s", what, res.OK, res.Reject, reason)
	}
	if len(res.Events) != 0 {
		t.Fatalf("%s: rejected call emitted events %v", what, res.Events)
	}
}

func expectState(t *testing.T, m *Manager, txnID int, want State) {
	t.Helper()
	if got := m.txns[txnID].state; got != want {
		t.Fatalf("t%d state = %s, want %s", txnID, got, want)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, k := range []int{-2, -1, 0, 65, 100} {
		if _, err := New(k); err != ErrInvalidKeyCount {
			t.Fatalf("New(%d): err = %v, want ErrInvalidKeyCount", k, err)
		}
	}
	for _, k := range []int{1, 2, 63, 64} {
		if _, err := New(k); err != nil {
			t.Fatalf("New(%d): unexpected err %v", k, err)
		}
	}
}

func TestBeginIDs(t *testing.T) {
	m := mustNew(t, 3)
	for want := 1; want <= 5; want++ {
		if got := m.Begin(); got != want {
			t.Fatalf("Begin() = %d, want %d", got, want)
		}
	}
}

func TestRejections(t *testing.T) {
	m := mustNew(t, 2)

	expectReject(t, "read unknown txn", m.Read(42, 0), RejectNoSuchTxn)
	expectReject(t, "write unknown txn", m.Write(42, 0, 1), RejectNoSuchTxn)
	expectReject(t, "commit unknown txn", m.Commit(42), RejectNoSuchTxn)
	expectReject(t, "abort unknown txn", m.Abort(42), RejectNoSuchTxn)

	t1 := m.Begin()
	expectReject(t, "read negative key", m.Read(t1, -1), RejectBadKey)
	expectReject(t, "read key == K", m.Read(t1, 2), RejectBadKey)
	expectReject(t, "write key > K", m.Write(t1, 5, 1), RejectBadKey)

	expectEvents(t, "t1 write k0", m.Write(t1, 0, 7), []Event{grantEvent(t1, 0, X, 7)})

	t2 := m.Begin()
	expectEvents(t, "t2 write queued", m.Write(t2, 0, 9), nil)
	expectState(t, m, t2, Waiting)

	expectReject(t, "waiting txn read", m.Read(t2, 0), RejectBadState)
	expectReject(t, "waiting txn write", m.Write(t2, 1, 1), RejectBadState)
	expectReject(t, "waiting txn commit", m.Commit(t2), RejectBadState)
	expectReject(t, "state checked before key", m.Read(t2, 99), RejectBadState)

	expectEvents(t, "abort waiting txn", m.Abort(t2), nil)
	expectState(t, m, t2, Aborted)
	expectReject(t, "abort aborted txn", m.Abort(t2), RejectBadState)
	expectReject(t, "aborted txn read", m.Read(t2, 0), RejectBadState)

	expectEvents(t, "t1 commit", m.Commit(t1), []Event{
		grantEvent(t1, 0, C, 0),
		commitEvent(t1, WriteEntry{Key: 0, Value: 7}),
	})
	expectState(t, m, t1, Committed)
	expectReject(t, "committed txn read", m.Read(t1, 0), RejectBadState)
	expectReject(t, "committed txn commit", m.Commit(t1), RejectBadState)
	expectReject(t, "committed txn abort", m.Abort(t1), RejectBadState)

	// None of the rejected calls above may have altered the state: a
	// fresh reader must still observe the committed value 7 on k0.
	t3 := m.Begin()
	res := m.Read(t3, 0)
	if !res.OK || res.Value != 7 {
		t.Fatalf("read after rejections: got %+v, want value 7", res)
	}
}

// TestConversionBeatsNormalWrite is the worked example from the spec:
// the conversion request of a committing transaction is inserted ahead
// of an already queued ordinary write.
func TestConversionBeatsNormalWrite(t *testing.T) {
	m := mustNew(t, 1)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()

	res := m.Read(t2, 0)
	if !res.OK || res.Value != 0 {
		t.Fatalf("t2 read: %+v, want value 0", res)
	}
	expectEvents(t, "t1 write 7", m.Write(t1, 0, 7), []Event{grantEvent(t1, 0, X, 7)})
	expectEvents(t, "t3 write queued", m.Write(t3, 0, 9), nil)
	expectState(t, m, t3, Waiting)

	// t1's C conflicts with t2's S and jumps the queue ahead of t3's X.
	res = m.Commit(t1)
	if res.Deadlock {
		t.Fatalf("t1 commit must not deadlock, got %+v", res)
	}
	expectEvents(t, "t1 commit queued", res, nil)
	expectState(t, m, t1, Committing)
	wantQueue := []request{
		{txn: t1, mode: C, conv: true},
		{txn: t3, mode: X, val: 9},
	}
	if !reflect.DeepEqual(m.queues[0], wantQueue) {
		t.Fatalf("queue = %+v, want %+v", m.queues[0], wantQueue)
	}

	// A new reader may not overtake the queued conversion even though S
	// is compatible with t2's S and t1's X.
	expectEvents(t, "t4 read queued", m.Read(t4, 0), nil)
	expectState(t, m, t4, Waiting)

	// t2 commits and releases its S: t1 certifies and commits with 7,
	// then t3 gets X and t4 gets S reading the new committed value 7.
	expectEvents(t, "t2 commit cascade", m.Commit(t2), []Event{
		commitEvent(t2),
		grantEvent(t1, 0, C, 0),
		commitEvent(t1, WriteEntry{Key: 0, Value: 7}),
		grantEvent(t3, 0, X, 9),
		grantEvent(t4, 0, S, 7),
	})
	expectState(t, m, t1, Committed)
	expectState(t, m, t3, Active)
	expectState(t, m, t4, Active)
	if m.committed[0] != 7 {
		t.Fatalf("committed[0] = %d, want 7", m.committed[0])
	}
}

// TestCommitDeadlockMutualReads: two transactions each read the key the
// other one is going to write; their C conversions then wait on each
// other and the second committer is aborted by the deadlock check.
func TestCommitDeadlockMutualReads(t *testing.T) {
	m := mustNew(t, 2)
	t1 := m.Begin()
	t2 := m.Begin()

	expectEvents(t, "t1 read k1", m.Read(t1, 1), []Event{grantEvent(t1, 1, S, 0)})
	expectEvents(t, "t1 write k0", m.Write(t1, 0, 10), []Event{grantEvent(t1, 0, X, 10)})
	expectEvents(t, "t2 read k0", m.Read(t2, 0), []Event{grantEvent(t2, 0, S, 0)})
	// S and X are compatible, so t2 may write k1 while t1 holds S on it.
	expectEvents(t, "t2 write k1", m.Write(t2, 1, 20), []Event{grantEvent(t2, 1, X, 20)})

	// t1's C on k0 conflicts with t2's S: queued, t1 committing.
	res := m.Commit(t1)
	if res.Deadlock {
		t.Fatalf("t1 commit must not deadlock yet: %+v", res)
	}
	expectEvents(t, "t1 commit queued", res, nil)
	expectState(t, m, t1, Committing)

	// t2's C on k1 conflicts with t1's S; the cycle t2 -> t1 -> t2
	// aborts t2, which releases its S on k0 and lets t1 certify.
	res = m.Commit(t2)
	if !res.OK || !res.Deadlock {
		t.Fatalf("t2 commit: got %+v, want deadlock abort", res)
	}
	expectEvents(t, "t2 deadlock cascade", res, []Event{
		grantEvent(t1, 0, C, 0),
		commitEvent(t1, WriteEntry{Key: 0, Value: 10}),
	})
	expectState(t, m, t1, Committed)
	expectState(t, m, t2, Aborted)
	if m.committed[0] != 10 || m.committed[1] != 0 {
		t.Fatalf("committed = %v, want [10 0]", m.committed)
	}
}

// TestCycleViaQueuePredecessors: the cycle is only closed by an edge to
// a queue predecessor, not by granted locks alone.
func TestCycleViaQueuePredecessors(t *testing.T) {
	m := mustNew(t, 2)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	expectEvents(t, "t3 write k1", m.Write(t3, 1, 31), []Event{grantEvent(t3, 1, X, 31)})
	expectEvents(t, "t1 write k0", m.Write(t1, 0, 10), []Event{grantEvent(t1, 0, X, 10)})
	expectEvents(t, "t2 write k0 queued", m.Write(t2, 0, 20), nil)
	// t3's S on k0 is compatible with t1's X but may not overtake t2.
	expectEvents(t, "t3 read k0 queued", m.Read(t3, 0), nil)

	// t1's X on k1 conflicts with t3's X. The wait cycle is
	// t1 -> t3 (granted X on k1), t3 -> t2 (queue predecessor on k0),
	// t2 -> t1 (granted X on k0). Without the predecessor edge t3 would
	// have no outgoing edge at all.
	res := m.Write(t1, 1, 11)
	if !res.OK || !res.Deadlock {
		t.Fatalf("t1 write k1: got %+v, want deadlock abort", res)
	}
	expectEvents(t, "t1 abort cascade", res, []Event{
		grantEvent(t2, 0, X, 20),
		grantEvent(t3, 0, S, 0),
	})
	expectState(t, m, t1, Aborted)
	expectState(t, m, t2, Active)
	expectState(t, m, t3, Active)
}

// TestDeadlockAbortCascadeAscending: the releases of an aborted
// transaction are processed strictly in ascending key order.
func TestDeadlockAbortCascadeAscending(t *testing.T) {
	m := mustNew(t, 3)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()
	t5 := m.Begin()
	t6 := m.Begin()

	expectEvents(t, "t1 write k2", m.Write(t1, 2, 32), []Event{grantEvent(t1, 2, X, 32)})
	expectEvents(t, "t1 write k0", m.Write(t1, 0, 30), []Event{grantEvent(t1, 0, X, 30)})
	expectEvents(t, "t2 read k0", m.Read(t2, 0), []Event{grantEvent(t2, 0, S, 0)})
	expectEvents(t, "t3 write k0 queued", m.Write(t3, 0, 33), nil)
	expectEvents(t, "t4 read k2", m.Read(t4, 2), []Event{grantEvent(t4, 2, S, 0)})
	expectEvents(t, "t5 write k2 queued", m.Write(t5, 2, 35), nil)
	expectEvents(t, "t6 write k1", m.Write(t6, 1, 61), []Event{grantEvent(t6, 1, X, 61)})
	expectEvents(t, "t6 write k0 queued", m.Write(t6, 0, 36), nil)

	// t1's X on k1 conflicts with t6's X; cycle t1 -> t6 -> t1 aborts
	// t1. Its locks on k0 and k2 are released in ascending key order:
	// t3's X on k0 is granted before t5's X on k2.
	res := m.Write(t1, 1, 31)
	if !res.OK || !res.Deadlock {
		t.Fatalf("t1 write k1: got %+v, want deadlock abort", res)
	}
	expectEvents(t, "t1 abort cascade", res, []Event{
		grantEvent(t3, 0, X, 33),
		grantEvent(t5, 2, X, 35),
	})
	expectState(t, m, t3, Active)
	expectState(t, m, t5, Active)
	expectState(t, m, t6, Waiting) // t6 still blocked behind t3's X
}

// TestCertLockHeldWhileWaiting: a C lock granted mid-commit keeps
// blocking other transactions while the commit waits for another key.
func TestCertLockHeldWhileWaiting(t *testing.T) {
	m := mustNew(t, 2)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	expectEvents(t, "t2 read k1", m.Read(t2, 1), []Event{grantEvent(t2, 1, S, 0)})
	expectEvents(t, "t1 write k0", m.Write(t1, 0, 10), []Event{grantEvent(t1, 0, X, 10)})
	expectEvents(t, "t1 write k1", m.Write(t1, 1, 11), []Event{grantEvent(t1, 1, X, 11)})

	// k0 converts immediately; k1 conflicts with t2's S and is queued.
	expectEvents(t, "t1 commit", m.Commit(t1), []Event{grantEvent(t1, 0, C, 0)})
	expectState(t, m, t1, Committing)

	// The C on k0 is held while t1 waits: t3's read must queue even
	// though the queue on k0 is empty.
	expectEvents(t, "t3 read k0 blocked by C", m.Read(t3, 0), nil)
	expectState(t, m, t3, Waiting)

	expectEvents(t, "t2 commit releases", m.Commit(t2), []Event{
		commitEvent(t2),
		grantEvent(t1, 1, C, 0),
		commitEvent(t1, WriteEntry{Key: 0, Value: 10}, WriteEntry{Key: 1, Value: 11}),
		grantEvent(t3, 0, S, 10),
	})
	expectState(t, m, t1, Committed)
	expectState(t, m, t3, Active)
}

// TestAbortCommittingReleasesAll: aborting a committing transaction
// releases both the queued conversions and the C locks already granted.
func TestAbortCommittingReleasesAll(t *testing.T) {
	m := mustNew(t, 2)
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	expectEvents(t, "t2 read k0", m.Read(t2, 0), []Event{grantEvent(t2, 0, S, 0)})
	expectEvents(t, "t1 write k0", m.Write(t1, 0, 10), []Event{grantEvent(t1, 0, X, 10)})
	expectEvents(t, "t1 write k1", m.Write(t1, 1, 11), []Event{grantEvent(t1, 1, X, 11)})

	// k0's conversion is queued behind t2's S, k1's is granted.
	expectEvents(t, "t1 commit", m.Commit(t1), []Event{grantEvent(t1, 1, C, 0)})
	expectState(t, m, t1, Committing)

	// t3 is blocked by the C t1 already holds on k1.
	expectEvents(t, "t3 read k1 queued", m.Read(t3, 1), nil)
	expectState(t, m, t3, Waiting)

	expectReject(t, "committing txn read", m.Read(t1, 0), RejectBadState)
	expectReject(t, "committing txn commit", m.Commit(t1), RejectBadState)

	// Aborting t1 frees k1, so t3 is granted its read of the old
	// committed value 0; t1's buffered writes are discarded.
	expectEvents(t, "abort t1", m.Abort(t1), []Event{grantEvent(t3, 1, S, 0)})
	expectState(t, m, t1, Aborted)
	expectState(t, m, t3, Active)
	if len(m.txns[t1].locks) != 0 {
		t.Fatalf("t1 still holds locks: %v", m.txns[t1].locks)
	}
	if m.committed[0] != 0 || m.committed[1] != 0 {
		t.Fatalf("committed = %v, want [0 0]", m.committed)
	}
}

// TestReadOwnBufferNoLock: reading a key the transaction wrote returns
// the buffered value without taking any additional lock, and repeated
// writes just overwrite the buffer.
func TestReadOwnBufferNoLock(t *testing.T) {
	m := mustNew(t, 1)
	t1 := m.Begin()

	expectEvents(t, "t1 write 42", m.Write(t1, 0, 42), []Event{grantEvent(t1, 0, X, 42)})
	res := m.Read(t1, 0)
	if !res.OK || res.Value != 42 || len(res.Events) != 0 {
		t.Fatalf("t1 read own buffer: %+v, want value 42 and no events", res)
	}
	if got := len(m.granted[0]); got != 1 {
		t.Fatalf("granted locks on k0 = %d, want exactly 1 (the X)", got)
	}
	expectEvents(t, "t1 overwrite", m.Write(t1, 0, 43), nil)
	res = m.Read(t1, 0)
	if !res.OK || res.Value != 43 {
		t.Fatalf("t1 read overwritten buffer: %+v, want value 43", res)
	}

	// A second read while holding S also takes no new lock.
	t2 := m.Begin()
	expectEvents(t, "t2 read", m.Read(t2, 0), []Event{grantEvent(t2, 0, S, 0)})
	res = m.Read(t2, 0)
	if !res.OK || res.Value != 0 || len(res.Events) != 0 {
		t.Fatalf("t2 re-read with S: %+v, want value 0 and no events", res)
	}
	if got := len(m.granted[0]); got != 2 {
		t.Fatalf("granted locks on k0 = %d, want 2", got)
	}
}

// TestConversionInsertedAfterEarlierConversion pins the insertion rule
// directly: a conversion goes after every conversion already queued and
// before every ordinary request. (With X-X conflicts at most one
// conversion per key can occur through the public API, so the rule is
// exercised at the insertion primitive itself.)
func TestConversionInsertedAfterEarlierConversion(t *testing.T) {
	queue := []request{
		{txn: 1, mode: C, conv: true},
		{txn: 2, mode: X, val: 5},
		{txn: 3, mode: S},
	}
	got := insertConversion(queue, request{txn: 4, mode: C, conv: true})
	want := []request{
		{txn: 1, mode: C, conv: true},
		{txn: 4, mode: C, conv: true},
		{txn: 2, mode: X, val: 5},
		{txn: 3, mode: S},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("insertConversion = %+v, want %+v", got, want)
	}
}
