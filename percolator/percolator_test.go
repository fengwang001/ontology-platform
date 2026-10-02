package percolator

import (
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// TestSpecWalkthrough replays the exact example from the specification.
func TestSpecWalkthrough(t *testing.T) {
	s := NewStore()
	st1 := s.Begin()
	if st1 != 1 {
		t.Fatalf("first Begin = %d, want 1", st1)
	}
	mustOK(t, s.Prewrite(st1, []Mutation{{Key: "a", Kind: Put, Value: "x"}, {Key: "b", Kind: Put, Value: "y"}}, "a", 10, 0))
	cts, err := s.CommitPrimary(st1)
	mustOK(t, err)
	if cts != 2 {
		t.Fatalf("commitTs = %d, want 2", cts)
	}

	if st := s.Begin(); st != 3 {
		t.Fatalf("second Begin = %d, want 3", st)
	}
	got, err := s.Get("b", 3, 2)
	mustOK(t, err)
	if !got.Exists || got.Value != "y" {
		t.Fatalf("Get(b,3) = %+v, want y", got)
	}
	snap := s.Snapshot()
	if v := snap.Keys["b"].Versions; len(v) != 1 || v[0] != (Version{CommitTS: 2, StartTS: 1, Kind: Put, Value: "y"}) {
		t.Fatalf("b rolled forward wrong: %+v", v)
	}

	st4 := s.Begin()
	if st4 != 4 {
		t.Fatalf("third Begin = %d, want 4", st4)
	}
	mustOK(t, s.Prewrite(st4, []Mutation{{Key: "a", Kind: Put, Value: "p"}, {Key: "b", Kind: Put, Value: "q"}}, "a", 10, 5))

	st5 := s.Begin()
	if st5 != 5 {
		t.Fatalf("fourth Begin = %d, want 5", st5)
	}
	_, err = s.Get("a", 5, 8)
	wantErr(t, err, ErrKeyLocked)

	got, err = s.Get("a", 3, 8)
	mustOK(t, err)
	if !got.Exists || got.Value != "x" {
		t.Fatalf("Get(a,3) = %+v, want x (lock startTs 4 > rts 3)", got)
	}

	got, err = s.Get("b", 5, 15)
	mustOK(t, err)
	if !got.Exists || got.Value != "y" {
		t.Fatalf("Get(b,5) = %+v, want y", got)
	}
	snap = s.Snapshot()
	if snap.Keys["a"].Lock != nil {
		t.Fatalf("primary lock of txn 4 not removed at deadline")
	}
	if rb := snap.Keys["a"].Versions; len(rb) != 2 || rb[0].Kind != Rollback || rb[0].StartTS != 4 || rb[1] != (Version{CommitTS: 2, StartTS: 1, Kind: Put, Value: "x"}) {
		t.Fatalf("a missing rollback for 4: %+v", rb)
	}
	if snap.Keys["b"].Lock != nil {
		t.Fatalf("secondary lock of txn 4 not removed")
	}
	if rb := snap.Keys["b"].Versions; len(rb) != 2 || rb[0].Kind != Rollback || rb[0].StartTS != 4 || rb[1] != (Version{CommitTS: 2, StartTS: 1, Kind: Put, Value: "y"}) {
		t.Fatalf("b missing rollback for 4: %+v", rb)
	}

	st6 := s.Begin()
	if st6 != 6 {
		t.Fatalf("fifth Begin = %d, want 6", st6)
	}
	res, err := s.CheckTxnStatus("a", 6, 20)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("CheckTxnStatus(6) = %+v, want rolled back", res)
	}
	err = s.Prewrite(st6, []Mutation{{Key: "a", Kind: Put, Value: "z"}}, "a", 10, 21)
	wantErr(t, err, ErrAlreadyAbort)
}

// TestReadTimestampBoundary: lock startTS == rts blocks; startTS > rts
// does not block.
func TestReadTimestampBoundary(t *testing.T) {
	s := NewStore()
	st1 := s.Begin() // 1
	mustOK(t, s.Prewrite(st1, []Mutation{{Key: "a", Kind: Put, Value: "old"}}, "a", 100, 0))
	cts, err := s.CommitPrimary(st1)
	mustOK(t, err)
	if cts != 2 {
		t.Fatalf("commitTs = %d", cts)
	}
	st3 := s.Begin() // 3
	mustOK(t, s.Prewrite(st3, []Mutation{{Key: "a", Kind: Put, Value: "new"}}, "a", 100, 0))

	_, err = s.Get("a", 3, 1)
	wantErr(t, err, ErrKeyLocked)

	got, err := s.Get("a", 2, 1)
	mustOK(t, err)
	if !got.Exists || got.Value != "old" {
		t.Fatalf("Get(a,2) = %+v, want old", got)
	}

	// Lock of 3 survives untouched because it never blocked the read.
	if lk := s.Snapshot().Keys["a"].Lock; lk == nil || lk.StartTS != 3 {
		t.Fatalf("non-blocking lock must remain: %+v", lk)
	}
}

// TestDeadlineBoundary: now == deadline expires the lock.
func TestDeadlineBoundary(t *testing.T) {
	s := NewStore()
	st := s.Begin()
	mustOK(t, s.Prewrite(st, []Mutation{{Key: "a", Kind: Put, Value: "v"}}, "a", 10, 5))

	res, err := s.CheckTxnStatus("a", st, 14)
	mustOK(t, err)
	if res.Status != StatusLive {
		t.Fatalf("now 14 < deadline 15 should be live, got %v", res.Status)
	}
	res, err = s.CheckTxnStatus("a", st, 15)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("now 15 == deadline 15 should roll back, got %v", res.Status)
	}
}

// TestWriteConflictBoundary: commitTs > st conflicts; equal does not.
func TestWriteConflictBoundary(t *testing.T) {
	s := NewStore()
	st1 := s.Begin() // 1
	st2 := s.Begin() // 2
	mustOK(t, s.Prewrite(st2, []Mutation{{Key: "a", Kind: Put, Value: "y"}}, "a", 10, 0))
	cts, err := s.CommitPrimary(st2)
	mustOK(t, err)
	if cts != 3 {
		t.Fatalf("commitTs = %d, want 3", cts)
	}
	err = s.Prewrite(st1, []Mutation{{Key: "a", Kind: Put, Value: "x"}, {Key: "b", Kind: Put, Value: "z"}}, "a", 10, 1)
	wantErr(t, err, ErrWriteConflict)

	// With a monotonic oracle a fresh start timestamp is never below an
	// earlier commitTs; "commitTs == st" cannot arise (spec note), so the
	// st boundary equals the start timestamp itself: a reader rollback at
	// commitTS == st is a Rollback entry and never counts as conflict.
	s2 := NewStore()
	r1 := s2.Begin()
	mustOK(t, s2.Prewrite(r1, []Mutation{{Key: "a", Kind: Put, Value: "x"}}, "a", 10, 0))
	res, err := s2.CheckTxnStatus("a", r1, 10)
	mustOK(t, err)
	if res.Status != StatusRolledBack {
		t.Fatalf("expire status = %v", res.Status)
	}
	// Same st prewrite now sees the Rollback first (ErrAlreadyAbort), but
	// a *different* later st must not treat the Rollback as write conflict
	// on an otherwise clean key.
	r2 := s2.Begin()
	mustOK(t, s2.Prewrite(r2, []Mutation{{Key: "a", Kind: Put, Value: "ok"}}, "a", 10, 11))
}
