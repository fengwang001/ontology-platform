package percolator

import "testing"

func put(key, val string) Mutation { return Mutation{Key: key, Kind: Put, Value: val} }
func del(key string) Mutation      { return Mutation{Key: key, Kind: Delete} }

// TestPrewriteErrorPriority: rolled-back > locked > write-conflict within
// a key; the first offending key in batch order wins.
func TestPrewriteErrorPriority(t *testing.T) {
	// Key setup for st3 prewrite:
	//  k1: rollback for 3           -> ErrAlreadyAbort
	//  k2: another txn's lock        -> ErrKeyLocked
	//  k3: committed write > st3     -> ErrWriteConflict
	s := NewStore()
	// make a rollback for st3 on k1: Begin 1,2 then expire txn1? No —
	// rollback belongs to whichever st. We create st3 directly.
	_ = s.Begin() // 1
	_ = s.Begin() // 2
	st3 := s.Begin()
	res, err := s.CheckTxnStatus("k1", st3, 0)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("protective rollback = %v", res.Status)
	}
	// Lock on k2 from txn1 (fresh prewrite, long ttl).
	mustOK(t, s.Prewrite(1, []Mutation{put("k2", "v")}, "k2", 1_000_000, 0))
	// Committed write on k3 by txn2, commitTs 4 > st3? oracle: next is 4,
	// but 4 > 3 yes.
	mustOK(t, s.Prewrite(2, []Mutation{put("k3", "v")}, "k3", 1_000_000, 0))
	if cts, err := s.CommitPrimary(2); err != nil || cts != 4 {
		t.Fatalf("commitTs = %d, %v", cts, err)
	}

	// Priority across keys: k1 comes first -> already abort.
	err = s.Prewrite(st3, []Mutation{put("k1", "a"), put("k2", "b"), put("k3", "c")}, "k1", 10, 1)
	wantErr(t, err, ErrAlreadyAbort)

	// Without k1: k2 (locked) wins over k3 (conflict).
	err = s.Prewrite(st3, []Mutation{put("k2", "b"), put("k3", "c")}, "k2", 10, 1)
	wantErr(t, err, ErrKeyLocked)

	// Only k3: write conflict.
	err = s.Prewrite(st3, []Mutation{put("k3", "c")}, "k3", 10, 1)
	wantErr(t, err, ErrWriteConflict)
}

// TestBatchAtomic: a conflict on a later key leaves no locks on earlier keys.
func TestBatchAtomic(t *testing.T) {
	s := NewStore()
	holder := s.Begin()
	mustOK(t, s.Prewrite(holder, []Mutation{put("c", "z")}, "c", 1_000_000, 0))

	st := s.Begin()
	before := s.Snapshot()
	err := s.Prewrite(st, []Mutation{put("a", "1"), put("b", "2"), put("c", "3")}, "a", 10, 1)
	wantErr(t, err, ErrKeyLocked)

	after := s.Snapshot()
	if !snapshotsEqual(before, after) {
		t.Fatalf("rejected prewrite changed state:\nbefore=%+v\nafter =%+v", before, after)
	}
	if _, ok := after.Keys["a"]; ok {
		t.Fatalf("key a should never be created on failed prewrite")
	}
}

// TestReaderRollForwardUsesPrimaryCommitTs: secondary commit uses the
// primary's commitTs, never a newly allocated timestamp.
func TestReaderRollForwardUsesPrimaryCommitTs(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{put("a", "x"), put("b", "y"), put("c", "z")}, "a", 10, 0))
	cts, err := s.CommitPrimary(st)
	mustOK(t, err)
	oracleAfterCommit := s.Snapshot().Oracle

	got, err := s.Get("b", 100, 1)
	mustOK(t, err)
	if !got.Exists || got.Value != "y" {
		t.Fatalf("b = %+v", got)
	}
	snap := s.Snapshot()
	if snap.Oracle != oracleAfterCommit {
		t.Fatalf("reader roll-forward allocated a timestamp: %d -> %d", oracleAfterCommit, snap.Oracle)
	}
	if v := snap.Keys["b"].Versions[0]; v.CommitTS != cts {
		t.Fatalf("b commitTs = %d, want primary %d", v.CommitTS, cts)
	}
	if snap.Keys["b"].Lock != nil {
		t.Fatalf("b lock not consumed")
	}

	// CommitKeys then skips b (already resolved) and commits c identically.
	mustOK(t, s.CommitKeys(st, []string{"b", "c"}))
	for _, key := range []string{"a", "b", "c"} {
		v := s.Snapshot().Keys[key].Versions[0]
		if v.CommitTS != cts || v.StartTS != st {
			t.Fatalf("%s = %+v, want commitTs %d", key, v, cts)
		}
	}
}

// TestPartialCommitVisibility: with only some secondaries committed,
// readers either see all keys or none, and visible versions share commitTs.
func TestPartialCommitVisibility(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{put("a", "1"), put("b", "2"), put("c", "3")}, "a", 10, 0))
	cts, err := s.CommitPrimary(st)
	mustOK(t, err)
	// Commit only b explicitly; c still locked.
	mustOK(t, s.CommitKeys(st, []string{"b"}))

	// rts below cts: nothing visible even though b was committed.
	for _, key := range []string{"a", "b", "c"} {
		got, err := s.Get(key, cts-1, 1)
		mustOK(t, err)
		if got.Exists {
			t.Fatalf("%s visible below commitTs", key)
		}
	}
	// rts at/above cts: resolving c via the primary yields the same cts.
	values := map[string]string{}
	for _, key := range []string{"a", "b", "c"} {
		got, err := s.Get(key, cts, 2)
		mustOK(t, err)
		if !got.Exists {
			t.Fatalf("%s missing at rts %d", key, cts)
		}
		values[key] = got.Value
	}
	if values["a"] != "1" || values["b"] != "2" || values["c"] != "3" {
		t.Fatalf("partial commit values = %+v", values)
	}
	for _, key := range []string{"a", "b", "c"} {
		v := s.Snapshot().Keys[key].Versions[0]
		if v.CommitTS != cts {
			t.Fatalf("%s commitTs %d != %d", key, v.CommitTS, cts)
		}
	}
}

// TestPrimaryLockLostCommit: once the primary lock is gone (expired by a
// reader), CommitPrimary fails ErrLockLost and the txn may still Abort.
func TestPrimaryLockLostCommit(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{put("a", "x"), put("b", "y")}, "a", 5, 0))
	res, err := s.CheckTxnStatus("a", st, 5)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("status = %v", res.Status)
	}
	_, err = s.CommitPrimary(st)
	wantErr(t, err, ErrLockLost)

	// Reader rollback does not change txn state: Abort still succeeds and
	// leaves a Rollback on every key.
	mustOK(t, s.Abort(st))
	snap := s.Snapshot()
	for _, key := range []string{"a", "b"} {
		k := snap.Keys[key]
		if k.Lock != nil {
			t.Fatalf("%s lock remains", key)
		}
		if !k.Versions[0].hasRollbackOf(st) {
			t.Fatalf("%s missing rollback for %d: %+v", key, st, k.Versions)
		}
	}
}

// TestProtectiveRollbackBlocksLatePrewrite mirrors the spec ending.
func TestProtectiveRollbackBlocksLatePrewrite(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	res, err := s.CheckTxnStatus("a", st, 0)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("status = %v", res.Status)
	}
	err = s.Prewrite(st, []Mutation{put("a", "z")}, "a", 10, 1)
	wantErr(t, err, ErrAlreadyAbort)
}

// TestRejectedOpsNoSideEffects snapshots state around every illegal call.
func TestRejectedOpsNoSideEffects(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{put("a", "x")}, "a", 10, 5))

	skewFresh := s.Begin() // existing fresh txn for clock-skew probes
	skewFresh2 := s.Begin()

	type probe struct {
		name string
		fn   func() error
		want error
	}
	probes := []probe{
		{"prewrite empty batch", func() error { return s.Prewrite(st, nil, "a", 10, 6) }, ErrInvalidArg},
		{"prewrite dup keys", func() error { return s.Prewrite(999, []Mutation{put("a", "1"), put("a", "2")}, "a", 10, 6) }, ErrInvalidArg},
		{"prewrite missing primary", func() error { return s.Prewrite(999, []Mutation{put("a", "1")}, "b", 10, 6) }, ErrInvalidArg},
		{"prewrite bad ttl", func() error { return s.Prewrite(999, []Mutation{put("b", "1")}, "b", 0, 6) }, ErrInvalidArg},
		{"prewrite no such txn", func() error { return s.Prewrite(42, []Mutation{put("b", "1")}, "b", 10, 6) }, ErrNoSuchTxn},
		{"prewrite twice", func() error { return s.Prewrite(st, []Mutation{put("b", "1")}, "b", 10, 6) }, ErrTxnState},
		{"prewrite bad now", func() error { return s.Prewrite(998, []Mutation{put("b", "1")}, "b", 10, maxTime+1) }, ErrInvalidTime},
		{"prewrite clock skew", func() error {
			return s.Prewrite(skewFresh, []Mutation{put("b", "1")}, "b", 10, 4)
		}, ErrClockSkew},
		{"commit no such txn", func() error { _, e := s.CommitPrimary(77); return e }, ErrNoSuchTxn},
		{"commit fresh txn", func() error {
			_, e := s.CommitPrimary(skewFresh2)
			return e
		}, ErrTxnState},
		{"commitkeys not committed", func() error { return s.CommitKeys(st, []string{"a"}) }, ErrTxnState},
		{"abort no such txn", func() error { return s.Abort(78) }, ErrNoSuchTxn},
		{"abort fresh txn", func() error {
			return s.Abort(skewFresh2)
		}, ErrTxnState},
		{"check empty primary", func() error { _, e := s.CheckTxnStatus("", 1, 6); return e }, ErrInvalidArg},
		{"check bad st", func() error { _, e := s.CheckTxnStatus("a", 0, 6); return e }, ErrInvalidArg},
		{"check bad now", func() error { _, e := s.CheckTxnStatus("a", 1, -1); return e }, ErrInvalidTime},
		{"check clock skew", func() error { _, e := s.CheckTxnStatus("a", 1, 3); return e }, ErrClockSkew},
		{"get empty key", func() error { _, e := s.Get("", 1, 6); return e }, ErrInvalidArg},
		{"get negative rts", func() error { _, e := s.Get("a", -1, 6); return e }, ErrInvalidArg},
		{"get bad now", func() error { _, e := s.Get("a", 1, maxTime+1); return e }, ErrInvalidTime},
		{"get clock skew", func() error { _, e := s.Get("a", 1, 4); return e }, ErrClockSkew},
	}
	for _, p := range probes {
		before := s.Snapshot()
		err := p.fn()
		wantErr(t, err, p.want)
		after := s.Snapshot()
		if !snapshotsEqual(before, after) {
			t.Fatalf("%s changed state despite rejection", p.name)
		}
	}
}

// TestDeleteInvisible treats Delete and missing versions identically.
func TestDeleteInvisible(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{del("a")}, "a", 10, 0))
	cts, err := s.CommitPrimary(st)
	mustOK(t, err)
	got, err := s.Get("a", cts, 1)
	mustOK(t, err)
	if got.Exists {
		t.Fatalf("Delete version readable: %+v", got)
	}
	got, err = s.Get("never-written", cts, 1)
	mustOK(t, err)
	if got.Exists {
		t.Fatalf("missing key reported exists")
	}
}
