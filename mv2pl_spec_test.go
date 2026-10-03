package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func modeName(m Mode) string {
	switch m {
	case S:
		return "S"
	case X:
		return "X"
	case C:
		return "C"
	}
	return "?"
}

func statusName(s TxnStatus) string {
	switch s {
	case Active:
		return "active"
	case Waiting:
		return "waiting"
	case Committing:
		return "committing"
	case Committed:
		return "committed"
	case Aborted:
		return "aborted"
	}
	return "?"
}

func describeResult(tag string, r Result) string {
	s := tag + " -> "
	switch {
	case r.Rejected:
		s += "REJECTED(" + r.RejectErr.Error() + ")"
	case r.Deadlock:
		s += "DEADLOCK"
	default:
		s += "OK status=" + statusName(r.Status)
		if tag == "Read" {
			s += fmt.Sprintf(" value=%d", r.Value)
		}
	}
	for _, e := range r.Events {
		if e.Kind == Grant {
			s += fmt.Sprintf(" | grant T%d k%d %s(v=%d) %s",
				e.Txn, e.Key, modeName(e.Mode), e.Value, statusName(e.Status))
		} else {
			s += fmt.Sprintf(" | commit T%d k%d value=%d", e.Txn, e.Key, e.Value)
		}
	}
	return s
}

func assertStatus(t *testing.T, r Result, want TxnStatus) {
	t.Helper()
	if r.Rejected || r.Deadlock || r.Status != want {
		t.Fatalf("want status %s, got rejected=%v deadlock=%v status=%s",
			statusName(want), r.Rejected, r.Deadlock, statusName(r.Status))
	}
}

func TestInvalidK(t *testing.T) {
	for _, k := range []int{0, -1, 65, 100} {
		if _, err := NewManager(k); !errors.Is(err, ErrInvalidK) {
			t.Fatalf("K=%d: want ErrInvalidK, got %v", k, err)
		}
	}
	if _, err := NewManager(1); err != nil {
		t.Fatal(err)
	}
}

func TestRejectionOrder(t *testing.T) {
	m, _ := NewManager(2)

	if r := m.Read(99, 1); !errors.Is(r.RejectErr, ErrUnknownTxn) {
		t.Fatalf("unknown txn: %v", r.RejectErr)
	}
	t1 := m.Begin().Txn
	m.Write(t1, 1, 1)
	m.Commit(t1)
	if r := m.Read(t1, 1); !errors.Is(r.RejectErr, ErrTxnNotActive) {
		t.Fatalf("committed txn: %v", r.RejectErr)
	}

	t2 := m.Begin().Txn
	t3 := m.Begin().Txn
	m.Write(t2, 1, 1)
	assertStatus(t, m.Write(t3, 1, 2), Waiting)
	// State check precedes key-range check.
	if r := m.Read(t3, 5); !errors.Is(r.RejectErr, ErrTxnNotActive) {
		t.Fatalf("waiting txn state check precedes key check: %v", r.RejectErr)
	}
	if r := m.Read(t2, 9); !errors.Is(r.RejectErr, ErrKeyOutOfRange) {
		t.Fatalf("active txn bad key: %v", r.RejectErr)
	}
	if r := m.Abort(t1); !errors.Is(r.RejectErr, ErrTxnNotAbortable) {
		t.Fatalf("already committed abort: %v", r.RejectErr)
	}
	// The rejected calls must not change state: aborting waiting T3 works.
	if r := m.Abort(t3); !r.OK || r.Status != Aborted {
		t.Fatalf("abort waiting: %+v", r)
	}
}

// Specification worked example: the conversion jumps ahead of a queued
// writer, and a new reader cannot overtake the queued certification.
func TestSpecExample(t *testing.T) {
	m, _ := NewManager(1)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn
	t3 := m.Begin().Txn
	t4 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 7), Active)
	assertStatus(t, m.Read(t2, 1), Active) // S compatible with X: reads committed
	assertStatus(t, m.Write(t3, 1, 9), Waiting)
	assertStatus(t, m.Commit(t1), Committing)
	assertStatus(t, m.Read(t4, 1), Waiting) // queue non-empty -> cannot overtake

	r := m.Commit(t2)
	want := []Event{
		{Kind: CommitEvent, Txn: t2, Key: -1},
		{Kind: CommitEvent, Txn: t1, Key: 1, Value: 7},
		{Kind: Grant, Txn: t3, Key: 1, Mode: X},
		{Kind: Grant, Txn: t4, Key: 1, Mode: S, Value: 7},
	}
	if len(r.Events) != len(want) {
		t.Fatalf("want %d events, got %+v", len(want), r.Events)
	}
	for i, w := range want {
		got := r.Events[i]
		if got.Kind != w.Kind || got.Txn != w.Txn || got.Key != w.Key || got.Value != w.Value {
			t.Fatalf("event %d: want %+v, got %+v", i, w, got)
		}
		if w.Kind == Grant && got.Mode != w.Mode {
			t.Fatalf("event %d mode: want %s, got %s", i, modeName(w.Mode), modeName(got.Mode))
		}
	}
	t.Log(describeResult("Commit", r))
}

// A conversion overtakes queued normal requests (writers/readers) and
// certification cannot be starved by new readers.
func TestConversionOvertakesNormalRequests(t *testing.T) {
	m, _ := NewManager(1)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn
	t3 := m.Begin().Txn
	t4 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 7), Active)
	assertStatus(t, m.Read(t2, 1), Active)
	assertStatus(t, m.Write(t3, 1, 9), Waiting)
	assertStatus(t, m.Commit(t1), Committing) // [T1:C, T3:X]
	assertStatus(t, m.Read(t4, 1), Waiting)   // [T1:C, T3:X, T4:S]

	r := m.Commit(t2)
	want := []Event{
		{Kind: CommitEvent, Txn: t2, Key: -1},
		{Kind: CommitEvent, Txn: t1, Key: 1, Value: 7},
		{Kind: Grant, Txn: t3, Key: 1, Mode: X},
		{Kind: Grant, Txn: t4, Key: 1, Mode: S, Value: 7},
	}
	if len(r.Events) != len(want) {
		t.Fatalf("events: %+v", r.Events)
	}
	for i, w := range want {
		g := r.Events[i]
		if g.Kind != w.Kind || g.Txn != w.Txn || g.Key != w.Key ||
			g.Value != w.Value || (w.Kind == Grant && g.Mode != w.Mode) {
			t.Fatalf("event %d want %+v got %+v", i, w, g)
		}
	}
	t.Log(describeResult("Commit", r))
}

// Direct check of the conversion-insertion position using a prepared state
// whose only flaw is the inherent two-C cycle: after the requester is killed
// the surviving entries keep their positions and pump in strict FIFO.
func TestConversionInsertionPosition(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn
	t3 := m.Begin().Txn
	tw := m.Begin().Txn

	tr1 := m.txns[t1]
	tr1.status = Committing
	tr1.buf[0] = 11
	tr1.buf[1] = 12
	tr1.locks[1] = C
	m.keys[1].granted[t1] = C
	tr2 := m.txns[t2]
	tr2.locks[0] = X
	tr2.buf[0] = 21
	tr3 := m.txns[t3]
	tr3.locks[0] = S
	trw := m.txns[tw]
	trw.status = Waiting
	ks := &m.keys[0]
	ks.granted[t2] = X
	ks.granted[t3] = S
	ks.queue = []entry{{txn: t1, mode: C}, {txn: tw, mode: X}}

	// Snapshot the position at which the C is inserted by performing the
	// insertion manually (identical code path as Commit), assert ordering,
	// then let Commit run: it keeps that position and deadlocks t2.
	m.enqueueAt(0, 1, entry{txn: t2, mode: C})
	q := m.keys[0].queue
	if len(q) != 3 ||
		q[0].txn != t1 || q[0].mode != C ||
		q[1].txn != t2 || q[1].mode != C ||
		q[2].txn != tw || q[2].mode != X {
		t.Fatalf("conversion inserted after last C, before normals: %+v", q)
	}
	// Remove the manually inserted entry so Commit re-inserts it itself.
	ks.queue = []entry{{txn: t1, mode: C}, {txn: tw, mode: X}}
	tr2.status = Active

	r := m.Commit(t2)
	if !r.Deadlock {
		t.Fatalf("inherent two-C cycle must kill requester, got %+v", r)
	}
	// t2 gone (locks and entry), t1:C and tw:X remain.
	q = m.keys[0].queue
	if len(q) != 2 || q[0].txn != t1 || q[0].mode != C ||
		q[1].txn != tw || q[1].mode != X {
		t.Fatalf("deadlock cleanup preserves positions: %+v", q)
	}
	if _, has := ks.granted[t2]; has {
		t.Fatal("requester locks must be released")
	}
	// Release t3 S: t1 certifies k1 then k2, tw gets X after.
	r = m.Commit(t3)
	if len(r.Events) != 4 ||
		r.Events[0].Txn != t3 ||
		r.Events[1].Kind != CommitEvent || r.Events[1].Txn != t1 || r.Events[1].Key != 1 ||
		r.Events[2].Kind != CommitEvent || r.Events[2].Txn != t1 || r.Events[2].Key != 2 ||
		r.Events[3].Kind != Grant || r.Events[3].Txn != tw {
		t.Fatalf("survivor cascade: %+v", r.Events)
	}
	t.Log(describeResult("Commit", r))
}

// Two transactions read each other's to-be-written key: the second
// conversion closes a commit-time deadlock.
func TestMutualCommitDeadlock(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn

	assertStatus(t, m.Read(t1, 1), Active)
	assertStatus(t, m.Read(t2, 2), Active)
	assertStatus(t, m.Write(t1, 2, 11), Active) // S/X compatible
	assertStatus(t, m.Write(t2, 1, 22), Active)

	assertStatus(t, m.Commit(t1), Committing) // C on k2 queued behind T2's S
	r := m.Commit(t2)                         // C on k1 closes the cycle
	if !r.Deadlock {
		t.Fatalf("want deadlock on second commit, got %+v", r)
	}
	// T2 is killed and its S on k2 release lets T1 certify value 11.
	if len(r.Events) != 1 || r.Events[0].Txn != t1 || r.Events[0].Value != 11 {
		t.Fatalf("survivor commit cascade: %+v", r.Events)
	}
	t.Log(describeResult("Commit", r))
}

// Cycle that exists only through predecessor queue entries:
// k2 holds T_c:X with [T_r:X] queued; k1 holds T_a:X and T_r:S with
// [T_b:X, T_c:S] queued; T_a's conversion is inserted at the head and
// closes T_a:C -> T_r(holder) -> T_r:X(k2) -> T_c(holder) -> T_c:S(k1)
// -> T_a:C (predecessor).
func TestCycleViaQueuePredecessor(t *testing.T) {
	m, _ := NewManager(2)
	ta := m.Begin().Txn
	tc := m.Begin().Txn
	tr := m.Begin().Txn
	tb := m.Begin().Txn

	assertStatus(t, m.Write(ta, 1, 1), Active)
	assertStatus(t, m.Write(tc, 2, 2), Active)
	assertStatus(t, m.Read(tr, 1), Active)
	assertStatus(t, m.Write(tr, 2, 3), Waiting)
	assertStatus(t, m.Write(tb, 1, 4), Waiting)
	assertStatus(t, m.Read(tc, 1), Waiting) // S ok with X but queue non-empty

	r := m.Commit(ta)
	if !r.Deadlock {
		t.Fatalf("conversion must close predecessor-edge cycle, got %+v", r)
	}
	// T_a abort releases X on k1: T_b gets X, then T_c's S is granted and
	// reads the committed value 0 (T_a's buffer 1 was discarded).
	if len(r.Events) != 2 ||
		r.Events[0].Txn != tb || r.Events[0].Mode != X ||
		r.Events[1].Txn != tc || r.Events[1].Mode != S || r.Events[1].Value != 0 {
		t.Fatalf("cascade after predecessor-cycle abort: %+v", r.Events)
	}
	t.Log(describeResult("Commit", r))
}

// A write/write deadlock aborts the requester; releasing its locks on several
// keys grants waiters in ascending key order.
func TestDeadlockAbortCascadeAscending(t *testing.T) {
	m, _ := NewManager(5)
	t1 := m.Begin().Txn
	t5 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 10), Active)
	assertStatus(t, m.Write(t1, 2, 20), Active)
	assertStatus(t, m.Write(t1, 3, 30), Active)
	assertStatus(t, m.Write(t1, 5, 50), Active)
	assertStatus(t, m.Write(t5, 4, 40), Active)
	assertStatus(t, m.Write(t5, 5, 51), Waiting) // t5 waits on t1 (k5)

	t4 := m.Begin().Txn
	t3 := m.Begin().Txn
	t2 := m.Begin().Txn
	assertStatus(t, m.Write(t4, 1, 11), Waiting)
	assertStatus(t, m.Write(t3, 2, 21), Waiting)
	assertStatus(t, m.Write(t2, 3, 31), Waiting)

	// t1 queues X on k4 behind t5: t1 -> t5 -> t5's k5 entry -> t1: cycle.
	r := m.Write(t1, 4, 41)
	if !r.Deadlock {
		t.Fatalf("want write-time deadlock, got %+v", r)
	}
	wantKeys := []int{1, 2, 3, 5}
	if len(r.Events) != 4 {
		t.Fatalf("4 grants expected, got %+v", r.Events)
	}
	for i, e := range r.Events {
		if e.Kind != Grant || e.Key != wantKeys[i] {
			t.Fatalf("event %d: want grant key %d, got %+v", i, wantKeys[i], e)
		}
	}
	t.Log(describeResult("Write", r))
}

// Already-obtained C locks remain held while the committing transaction keeps
// waiting on later keys, and still block newcomers.
func TestCommittingHoldsEarlierC(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 10), Active)
	assertStatus(t, m.Write(t1, 2, 20), Active)
	assertStatus(t, m.Read(t2, 2), Active)
	assertStatus(t, m.Commit(t1), Committing) // C granted k1, queued k2

	if got := m.keys[0].granted[t1]; got != C {
		t.Fatalf("k1 must keep holding C, got %s", modeName(got))
	}
	t3 := m.Begin().Txn
	assertStatus(t, m.Write(t3, 1, 11), Waiting) // blocked by the held C

	r := m.Commit(t2)
	var commitKeys []int
	for _, e := range r.Events {
		if e.Kind == CommitEvent && e.Txn == t1 {
			commitKeys = append(commitKeys, e.Key)
		}
	}
	if len(commitKeys) != 2 || commitKeys[0] != 1 || commitKeys[1] != 2 {
		t.Fatalf("T1 commit keys: %v events=%+v", commitKeys, r.Events)
	}
	last := r.Events[len(r.Events)-1]
	if last.Txn != t3 || last.Mode != X {
		t.Fatalf("T3 should be woken after certification releases, got %+v", last)
	}
	t.Log(describeResult("Commit", r))
}

// Abort of a committing transaction removes queued conversions and releases
// every held lock, discarding buffers.
func TestAbortCommitting(t *testing.T) {
	m, _ := NewManager(2)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 10), Active)
	assertStatus(t, m.Write(t1, 2, 20), Active)
	assertStatus(t, m.Read(t2, 2), Active)
	assertStatus(t, m.Commit(t1), Committing)

	r := m.Abort(t1)
	if !r.OK || r.Status != Aborted {
		t.Fatalf("abort committing: %+v", r)
	}
	if tr := m.txns[t1]; tr == nil || tr.status != Aborted {
		t.Fatal("aborted txn must remain as Aborted so later calls are rejected")
	}
	if r := m.Read(t1, 1); !r.Rejected {
		t.Fatalf("aborted txn cannot start new calls: %+v", r)
	}
	if len(m.keys[0].granted) != 0 || len(m.keys[1].granted) != 1 {
		t.Fatalf("locks after abort: k0=%v k1=%v", m.keys[0].granted, m.keys[1].granted)
	}
	if m.keys[0].committed != 0 || m.keys[1].committed != 0 {
		t.Fatal("buffers must be discarded, committed values unchanged")
	}
	t.Log(describeResult("Abort", r))
}

// Reading the own buffered value needs no new lock and sees latest writes.
func TestReadOwnBufferNoLock(t *testing.T) {
	m, _ := NewManager(1)
	t1 := m.Begin().Txn
	t2 := m.Begin().Txn

	assertStatus(t, m.Write(t1, 1, 5), Active)
	assertStatus(t, m.Read(t2, 1), Active) // committed value 0
	r := m.Read(t1, 1)
	if !r.OK || r.Value != 5 {
		t.Fatalf("own buffered read: %+v", r)
	}
	assertStatus(t, m.Write(t1, 1, 6), Active)
	r = m.Read(t1, 1)
	if r.Value != 6 {
		t.Fatalf("overwritten buffer: %+v", r)
	}
	if mode := m.keys[0].granted[t1]; mode != X {
		t.Fatalf("reading own buffer must not add/change a lock, got %s", modeName(mode))
	}
	assertStatus(t, m.Abort(t1), Aborted)
	r = m.Read(t2, 1)
	if r.Value != 0 {
		t.Fatalf("aborted buffer must never become committed, got %d", r.Value)
	}
	t.Log(describeResult("Read", r))
}
