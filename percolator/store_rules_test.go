package percolator

import (
	"errors"
	"testing"
)

func codeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func mustOK(t *testing.T, err error, ctx ...string) {
	t.Helper()
	if err != nil {
		msg := ""
		if len(ctx) > 0 {
			msg = ctx[0] + ": "
		}
		t.Fatalf("%sunexpected error %v", msg, err)
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	s := NewStore()

	st1 := s.Begin()
	if st1 != 1 {
		t.Fatalf("first Begin = %d, want 1", st1)
	}
	mustOK(t, s.Prewrite(st1, []Mutation{
		{Key: "a", Type: Put, Value: "x"},
		{Key: "b", Type: Put, Value: "y"},
	}, "a", 10, 0), "prewrite txn1")
	ct, err := s.CommitPrimary(st1)
	mustOK(t, err, "commit primary txn1")
	if ct != 2 {
		t.Fatalf("commitTs = %d, want 2", ct)
	}

	if st := s.Begin(); st != 3 {
		t.Fatalf("second Begin = %d, want 3", st)
	}
	found, val, err := s.Get("b", 3, 2)
	mustOK(t, err, "get b rolls forward")
	if !found || val != "y" {
		t.Fatalf("Get(b,3) = (%v,%q), want (true,y)", found, val)
	}
	if vs := s.VersionsOf("b"); len(vs) != 1 || vs[0].CommitTs != 2 || vs[0].Value != "y" {
		t.Fatalf("b rolled forward with wrong version: %+v", vs)
	}

	st4 := s.Begin()
	if st4 != 4 {
		t.Fatalf("third Begin = %d, want 4", st4)
	}
	mustOK(t, s.Prewrite(st4, []Mutation{
		{Key: "a", Type: Put, Value: "p"},
		{Key: "b", Type: Put, Value: "q"},
	}, "a", 10, 5), "prewrite txn4")

	_ = s.Begin() // st 5
	_, _, err = s.Get("a", 5, 8)
	if codeOf(err) != ErrLocked {
		t.Fatalf("Get(a,5,8) err = %v, want locked", err)
	}
	found, val, err = s.Get("a", 3, 8)
	mustOK(t, err, "get a at old rts ignores newer lock")
	if !found || val != "x" {
		t.Fatalf("Get(a,3) = (%v,%q), want (true,x)", found, val)
	}

	found, val, err = s.Get("b", 5, 15)
	mustOK(t, err, "get b at expire time reads old version")
	if !found || val != "y" {
		t.Fatalf("Get(b,5,15) = (%v,%q), want (true,y); txn4's lock rolled back", found, val)
	}
	found, val, err = s.Get("b", 5, 16)
	mustOK(t, err, "get b after rollback")
	if !found || val != "y" {
		t.Fatalf("Get(b,5) after rollback = (%v,%q), want (true,y)", found, val)
	}
	if _, locked := s.LockOf("a"); locked {
		t.Fatalf("primary lock of txn4 should be gone")
	}
	if _, locked := s.LockOf("b"); locked {
		t.Fatalf("secondary lock of txn4 should be gone")
	}

	st6 := s.Begin()
	status, _, err := s.CheckTxnStatus("a", st6, 20)
	mustOK(t, err, "protective check")
	if status != StatusRolledBack {
		t.Fatalf("status = %v, want rolled_back", status)
	}
	err = s.Prewrite(st6, []Mutation{{Key: "a", Type: Put, Value: "z"}}, "a", 10, 21)
	if codeOf(err) != ErrAlreadyAborted {
		t.Fatalf("late prewrite err = %v, want already_aborted", err)
	}
}

// A lock with startTs == rts blocks; startTs > rts is invisible to the read.
func TestLockStartTsBoundary(t *testing.T) {
	s := NewStore()
	st := s.Begin() // 1
	mustOK(t, s.Prewrite(st, []Mutation{{Key: "k", Type: Put, Value: "v"}}, "k", 100, 0), "prewrite")

	_, _, err := s.Get("k", 1, 50)
	if codeOf(err) != ErrLocked {
		t.Fatalf("startTs == rts: err = %v, want locked", err)
	}

	// No version exists, so at rts 0 the unblocked read returns absent.
	found, _, err := s.Get("k", 0, 51)
	mustOK(t, err, "startTs > rts must not block")
	if found {
		t.Fatalf("read at rts 0 unexpectedly found a value")
	}
	if _, locked := s.LockOf("k"); !locked {
		t.Fatalf("newer lock must remain untouched by older reader")
	}
}

// Expiry is inclusive: now == expire rolls the primary back.
func TestExpireExactlyAtNow(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{{Key: "k", Type: Put, Value: "v"}}, "k", 10, 5), "prewrite")
	status, _, err := s.CheckTxnStatus("k", st, 14)
	mustOK(t, err)
	if status != StatusLive {
		t.Fatalf("now 14 < expire 15: status = %v, want live", status)
	}
	status, _, err = s.CheckTxnStatus("k", st, 15)
	mustOK(t, err)
	if status != StatusRolledBack {
		t.Fatalf("now == expire: status = %v, want rolled_back", status)
	}
}

// Only committed versions with commitTs > st conflict; Rollback never does.
func TestWriteConflictBoundaryAndRollbackIgnored(t *testing.T) {
	s := NewStore()
	st1 := s.Begin() // 1
	mustOK(t, s.Prewrite(st1, []Mutation{{Key: "k", Type: Put, Value: "v1"}}, "k", 100, 0), "pw1")
	ct, _ := s.CommitPrimary(st1)
	if ct != 2 {
		t.Fatalf("commitTs = %d, want 2", ct)
	}

	// st 3 starts *before* committing anything else; commitTs of a newer
	// committed txn must be > 3 to conflict.
	st3 := s.Begin() // 3
	st4 := s.Begin() // 4
	mustOK(t, s.Prewrite(st4, []Mutation{{Key: "g", Type: Put, Value: "g"}}, "g", 100, 1), "pw4")
	ct4, _ := s.CommitPrimary(st4) // commitTs 5 > 3
	if ct4 != 5 {
		t.Fatalf("commitTs = %d, want 5", ct4)
	}
	if err := s.Prewrite(st3, []Mutation{{Key: "g", Type: Put, Value: "x"}}, "g", 100, 2); codeOf(err) != ErrWriteConflict {
		t.Fatalf("older txn st3 vs commitTs 5: want write_conflict, got %v", err)
	}

	// The pre-existing v1 on k has commitTs 2 which is not > 3: prewriting k
	// at st 3 must not conflict (assuming no foreign lock and no rollback).
	mustOK(t, s.Prewrite(st3, []Mutation{{Key: "k", Type: Put, Value: "v2"}}, "k", 100, 3), "commitTs 2 <= st 3 does not conflict")

	// The pre-existing committed v1 on k has commitTs 2 which is not > 3.
	// But to isolate "rollback does not conflict", use a key that only has
	// a rollback record.
	stRb := s.Begin() // 6
	mustOK(t, s.Prewrite(stRb, []Mutation{{Key: "rb", Type: Delete}}, "rb", 100, 3), "pw rb")
	mustOK(t, s.Abort(stRb), "abort rb")
	stNew := s.Begin() // 7
	mustOK(t, s.Prewrite(stNew, []Mutation{{Key: "rb", Type: Put, Value: "ok"}}, "rb", 100, 4), "rollback record must not conflict")
}

// Per-key priority: already-rolled-back beats locked beats write conflict.
func TestPrewritePerKeyPriority(t *testing.T) {
	// Rollback record for the prewriting txn itself beats a foreign lock.
	s := NewStore()
	victim := s.Begin() // 1
	holder := s.Begin() // 2
	mustOK(t, s.Prewrite(holder, []Mutation{{Key: "k", Type: Put, Value: "h"}}, "k", 100, 0), "holder locks k")
	status, _, err := s.CheckTxnStatus("k", victim, 1)
	mustOK(t, err)
	if status != StatusRolledBack {
		t.Fatalf("protective rollback status = %v", status)
	}
	err = s.Prewrite(victim, []Mutation{{Key: "k", Type: Put, Value: "v"}}, "k", 100, 2)
	if codeOf(err) != ErrAlreadyAborted {
		t.Fatalf("want already_aborted, got %v", err)
	}
}

// Foreign lock beats write conflict within one key.
func TestPrewriteLockBeatsConflict(t *testing.T) {
	s := NewStore()
	// Cross-key ordering: first key locked, later key conflicting -> lock wins.
	committed := s.Begin() // 1
	mustOK(t, s.Prewrite(committed, []Mutation{{Key: "cf", Type: Put, Value: "b"}}, "cf", 100, 0), "cf pw")
	_, _ = s.CommitPrimary(committed) // commitTs 2

	locker := s.Begin() // 3
	mustOK(t, s.Prewrite(locker, []Mutation{
		{Key: "lk", Type: Put, Value: "l"},
	}, "lk", 100, 1), "locker")

	victim := s.Begin() // 4; cf's commitTs 2 is not > 4, so add a newer commit
	bump := s.Begin()   // 5
	mustOK(t, s.Prewrite(bump, []Mutation{{Key: "cf", Type: Put, Value: "b2"}}, "cf", 100, 2), "bump pw")
	_, _ = s.CommitPrimary(bump) // commitTs 6 > victim 4 -> write conflict
	err := s.Prewrite(victim, []Mutation{
		{Key: "lk", Type: Put, Value: "x"}, // foreign lock encountered first
		{Key: "cf", Type: Put, Value: "y"}, // write conflict would follow
	}, "lk", 100, 3)
	if codeOf(err) != ErrKeyLocked {
		t.Fatalf("want key_locked reported before write_conflict, got %v", err)
	}
}

// Whole-batch prewrite is atomic: a conflict on a later key leaves no locks.
func TestPrewriteAtomicBatch(t *testing.T) {
	s := NewStore()
	blocker := s.Begin() // 1
	mustOK(t, s.Prewrite(blocker, []Mutation{{Key: "c", Type: Put, Value: "z"}}, "c", 100, 0), "blocker")
	ct, _ := s.CommitPrimary(blocker) // commitTs 2

	victim := s.Begin() // 3
	bump := s.Begin()   // 4
	mustOK(t, s.Prewrite(bump, []Mutation{{Key: "c", Type: Put, Value: "n"}}, "c", 100, 1), "bump")
	ctBump, _ := s.CommitPrimary(bump) // commitTs 5 > victim 3
	_ = ct
	_ = ctBump

	err := s.Prewrite(victim, []Mutation{
		{Key: "a", Type: Put, Value: "1"},
		{Key: "b", Type: Put, Value: "2"},
		{Key: "c", Type: Put, Value: "3"}, // conflict (commitTs 5 > 3)
	}, "a", 100, 2)
	if codeOf(err) != ErrWriteConflict {
		t.Fatalf("want write_conflict, got %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if lk, has := s.LockOf(k); k != "c" && has {
			t.Fatalf("key %s leaked lock %+v after failed batch", k, lk)
		}
	}
}

// Reader roll-forward uses the primary's commitTs, never a fresh timestamp.
func TestRollforwardUsesPrimaryCommitTs(t *testing.T) {
	s := NewStore()
	st := s.Begin() // 1
	mustOK(t, s.Prewrite(st, []Mutation{
		{Key: "p", Type: Put, Value: "p"},
		{Key: "s", Type: Put, Value: "s"},
	}, "p", 100, 0), "pw")
	ct, err := s.CommitPrimary(st) // commitTs 2
	mustOK(t, err, "commit primary")
	// Burn several oracle timestamps before the reader resolves the secondary.
	_ = s.Begin() // 3
	_ = s.Begin() // 4
	found, val, err := s.Get("s", 5, 1)
	mustOK(t, err, "get secondary")
	if !found || val != "s" {
		t.Fatalf("Get(s) = (%v,%q)", found, val)
	}
	vs := s.VersionsOf("s")
	if len(vs) != 1 || vs[0].CommitTs != ct {
		t.Fatalf("secondary committed at %+v, want commitTs %d", vs, ct)
	}
}

// Partial secondary commit: all keys become visible at the same commitTs to a
// reader at rts >= commitTs, and invisible to readers below it.
func TestPartialCommitConsistency(t *testing.T) {
	s := NewStore()
	st := s.Begin() // 1
	mustOK(t, s.Prewrite(st, []Mutation{
		{Key: "a", Type: Put, Value: "A"},
		{Key: "b", Type: Put, Value: "B"},
		{Key: "c", Type: Put, Value: "C"},
	}, "a", 100, 0), "pw")
	ct, _ := s.CommitPrimary(st) // ct 2
	mustOK(t, s.CommitKeys(st, []string{"b"}), "commit only b")

	// Reader below ct sees none of the transaction anywhere.
	for _, k := range []string{"a", "b", "c"} {
		found, _, err := s.Get(k, 1, 1)
		mustOK(t, err, "old read "+k)
		if found {
			t.Fatalf("reader at rts 1 must not see %s", k)
		}
	}

	// Reader at/above ct resolves a and c; all three now visible at ct.
	want := map[string]string{"a": "A", "b": "B", "c": "C"}
	for k, v := range want {
		found, got, err := s.Get(k, 5, 2)
		mustOK(t, err, "new read "+k)
		if !found || got != v {
			t.Fatalf("Get(%s) = (%v,%q), want %q", k, found, got, v)
		}
		for _, ver := range s.VersionsOf(k) {
			if ver.StartTs == st && ver.CommitTs != ct {
				t.Fatalf("%s version commitTs %d != primary %d", k, ver.CommitTs, ct)
			}
		}
	}
}

// A protective rollback on the primary blocks the transaction's later prewrite.
func TestProtectiveRollback(t *testing.T) {
	s := NewStore()
	st := s.Begin() // never prewritten
	status, _, err := s.CheckTxnStatus("p", st, 0)
	mustOK(t, err)
	if status != StatusRolledBack {
		t.Fatalf("status = %v", status)
	}
	err = s.Prewrite(st, []Mutation{{Key: "p", Type: Put, Value: "v"}}, "p", 10, 1)
	if codeOf(err) != ErrAlreadyAborted {
		t.Fatalf("want already_aborted, got %v", err)
	}
}

// CommitPrimary fails once the primary lock has been resolved away.
func TestCommitPrimaryLockLost(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{{Key: "p", Type: Put, Value: "v"}}, "p", 10, 0), "pw")
	status, _, err := s.CheckTxnStatus("p", st, 10)
	mustOK(t, err)
	if status != StatusRolledBack {
		t.Fatalf("status = %v", status)
	}
	_, err = s.CommitPrimary(st)
	if codeOf(err) != ErrLockLost {
		t.Fatalf("want lock_lost, got %v", err)
	}
}

// Abort clears locks and leaves exactly one Rollback version per key; it is
// allowed even when a reader already rolled some keys back.
func TestAbortLeavesRollbackPerKey(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{
		{Key: "p", Type: Put, Value: "P"},
		{Key: "s", Type: Put, Value: "S"},
	}, "p", 10, 0), "pw")

	// Reader expires the whole txn via the primary and cleans secondary s.
	_, _, err := s.Get("s", 5, 10)
	mustOK(t, err, "reader rollback")

	mustOK(t, s.Abort(st), "abort after reader rollback")
	for _, k := range []string{"p", "s"} {
		if _, locked := s.LockOf(k); locked {
			t.Fatalf("lock on %s survived abort", k)
		}
		n := 0
		for _, v := range s.VersionsOf(k) {
			if v.Type == Rollback && v.StartTs == st {
				n++
				if v.CommitTs != st {
					t.Fatalf("rollback commitTs %d != startTs %d", v.CommitTs, st)
				}
			}
		}
		if n != 1 {
			t.Fatalf("key %s has %d rollback records, want exactly 1", k, n)
		}
	}
}

// Rejected operations mutate nothing: oracle, watermark, locks and versions.
func TestRejectionChangesNothing(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{{Key: "p", Type: Put, Value: "v"}}, "p", 10, 5), "pw")

	// Invalid time ordering: out-of-range beats clock skew; neither mutates.
	cases := []func() error{
		func() error {
			return s.Prewrite(999, []Mutation{{Key: "x", Type: Put, Value: "1"}}, "x", 10, 4)
		}, // txn not found, oracle untouched
		func() error { return s.Prewrite(999, []Mutation{{Key: "x", Type: Put, Value: "1"}}, "x", 10, 6) },
		func() error { return s.Prewrite(st, []Mutation{{Key: "x", Type: Put, Value: "1"}}, "x", 10, 6) },
		func() error { _, e := s.CommitPrimary(999); return e },
		func() error { return s.CommitKeys(999, []string{"p"}) },
		func() error { return s.CommitKeys(st, []string{"p"}) }, // not committed
		func() error { return s.Abort(999) },
	}
	for i, c := range cases {
		if err := c(); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	snap := s.Snapshot()
	if snap.Oracle != st {
		t.Fatalf("oracle = %d, want %d", snap.Oracle, st)
	}
	if snap.Watermark != 5 {
		t.Fatalf("watermark = %d, want 5", snap.Watermark)
	}
	if lk, ok := s.LockOf("p"); !ok || lk.StartTs != st {
		t.Fatalf("primary lock altered: %+v ok=%v", lk, ok)
	}
	if vs := s.VersionsOf("p"); len(vs) != 0 {
		t.Fatalf("versions leaked: %+v", vs)
	}

	// CommitKeys with a foreign key is rejected without committing anything.
	_, err := s.CommitPrimary(st)
	mustOK(t, err, "commit primary")
	st2 := s.Begin()
	mustOK(t, s.Prewrite(st2, []Mutation{{Key: "p", Type: Put, Value: "w"}, {Key: "q", Type: Delete}}, "p", 10, 6), "pw2")
	ct2, err := s.CommitPrimary(st2)
	mustOK(t, err, "commit primary 2")
	err = s.CommitKeys(st2, []string{"q", "nope"})
	if codeOf(err) != ErrInvalidArgument {
		t.Fatalf("want invalid_argument, got %v", err)
	}
	if _, locked := s.LockOf("q"); !locked {
		t.Fatalf("q lock must remain after rejected CommitKeys")
	}
	// The failed commit did not consume a timestamp.
	if o := s.Snapshot().Oracle; o != ct2 {
		t.Fatalf("oracle moved after rejected commit: %d vs %d", o, ct2)
	}
}
