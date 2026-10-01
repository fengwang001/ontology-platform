package mvcc

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

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want error %v, got %v", want, err)
	}
}

func mustVisible(t *testing.T, s *Store, tuple string, x, c, snap int, want bool) {
	t.Helper()
	got, why, err := s.Explain(tuple, x, c, snap)
	mustOK(t, err)
	if got != want {
		t.Fatalf("Visible(%s,%d,%d,%d) = %v, want %v; reason: %s", tuple, x, c, snap, got, want, why)
	}
	t.Logf("Visible(%s,%d,%d,%d) = %v; %s", tuple, x, c, snap, got, why)
}

func TestCminEqualCurrentCommandInvisible(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("t", 1, 5))
	snap := s.Snapshot()
	mustVisible(t, s, "t", 1, 5, snap, false)
	mustVisible(t, s, "t", 1, 6, snap, true)
}

func TestOwnDeleteVisibleSameCommandInvisibleNext(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("t", 1, 0))
	mustOK(t, s.Delete("t", 1, 3))
	snap := s.Snapshot()
	mustVisible(t, s, "t", 1, 3, snap, true)
	mustVisible(t, s, "t", 1, 4, snap, false)
}

func TestAbortedSubInsertInvisibleAndDeleteOverwritable(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.BeginSub(2, 1))
	mustOK(t, s.Insert("ins", 2, 0))
	mustOK(t, s.Insert("victim", 1, 1))
	mustOK(t, s.Delete("victim", 2, 2))
	mustOK(t, s.Abort(2))
	snap := s.Snapshot()
	mustVisible(t, s, "ins", 1, 3, snap, false)
	mustVisible(t, s, "victim", 1, 3, snap, true)
	mustOK(t, s.Delete("victim", 1, 3))
	mustVisible(t, s, "victim", 1, 4, snap, false)
}

func TestParentAbortCascadesToSubCommittedChild(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.BeginSub(2, 1))
	mustOK(t, s.Insert("t", 2, 0))
	mustOK(t, s.CommitSub(2))
	mustOK(t, s.Abort(1))
	mustOK(t, s.Begin(3))
	snap := s.Snapshot()
	mustVisible(t, s, "t", 3, 0, snap, false)
	mustOK(t, s.Insert("u", 3, 0))
	mustOK(t, s.Delete("u", 3, 1))
	mustOK(t, s.Abort(3))
	mustOK(t, s.Begin(4))
	mustOK(t, s.Insert("u2", 4, 0))
}

func TestSnapshotSeesOnlyPreviouslyCommittedRoots(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("before", 1, 0))
	mustOK(t, s.Commit(1))
	snap := s.Snapshot()
	mustOK(t, s.Begin(2))
	mustOK(t, s.Insert("after", 2, 0))
	mustOK(t, s.Commit(2))
	mustOK(t, s.Begin(3))
	mustVisible(t, s, "before", 3, 0, snap, true)
	mustVisible(t, s, "after", 3, 0, snap, false)
	snap2 := s.Snapshot()
	mustVisible(t, s, "after", 3, 0, snap2, true)
}

func TestSubCommittedInvisibleToOtherTreesUntilRootCommits(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.BeginSub(2, 1))
	mustOK(t, s.Insert("t", 2, 0))
	mustOK(t, s.CommitSub(2))
	snap := s.Snapshot()
	mustOK(t, s.Begin(3))
	mustVisible(t, s, "t", 3, 0, snap, false)
	mustOK(t, s.Commit(1))
	snap2 := s.Snapshot()
	mustVisible(t, s, "t", 3, 0, snap2, true)
}

func TestErrorPrecedence(t *testing.T) {
	s := NewStore()
	mustErr(t, s.Begin(0), ErrTxIDNotPositive)
	mustErr(t, s.Begin(-3), ErrTxIDNotPositive)
	mustOK(t, s.Begin(1))
	mustErr(t, s.Begin(1), ErrTxExists)
	mustErr(t, s.BeginSub(0, 1), ErrTxIDNotPositive)
	mustErr(t, s.BeginSub(1, 1), ErrTxExists)
	mustErr(t, s.BeginSub(2, 99), ErrParentNotFound)
	mustOK(t, s.Begin(5))
	mustOK(t, s.Abort(5))
	mustErr(t, s.BeginSub(2, 5), ErrParentNotRunning)
	mustOK(t, s.BeginSub(2, 1))
	mustOK(t, s.BeginSub(3, 2))

	mustErr(t, s.CommitSub(99), ErrTxNotFound)
	mustErr(t, s.CommitSub(5), ErrTxNotRunning)
	mustErr(t, s.CommitSub(1), ErrTxTypeMismatch)
	mustErr(t, s.Commit(2), ErrTxTypeMismatch)
	mustErr(t, s.CommitSub(2), ErrTxHasRunningDescendants)
	mustErr(t, s.Commit(1), ErrTxHasRunningDescendants)
	mustOK(t, s.CommitSub(3))
	mustOK(t, s.CommitSub(2))
	mustOK(t, s.Commit(1))
	mustErr(t, s.Abort(1), ErrTxNotRunning)
	mustErr(t, s.Abort(99), ErrTxNotFound)
}

func TestWriteCommandIDRules(t *testing.T) {
	s := NewStore()
	mustErr(t, s.Insert("t", 9, 0), ErrTxNotFound)
	mustOK(t, s.Begin(1))
	mustOK(t, s.Begin(2))
	mustOK(t, s.Abort(2))
	mustErr(t, s.Insert("t", 2, 0), ErrTxNotRunning)
	mustErr(t, s.Insert("t", 1, -1), ErrNegativeCommandID)
	mustOK(t, s.Insert("t", 1, 5))
	mustErr(t, s.Insert("u", 1, 4), ErrCommandIDTooSmall)
	mustErr(t, s.Delete("t", 1, 4), ErrCommandIDTooSmall)
	mustErr(t, s.Insert("t", 1, 5), ErrTupleExists)
	mustErr(t, s.Delete("nope", 1, 5), ErrTupleNotFound)
	mustOK(t, s.Delete("t", 1, 5))
	mustErr(t, s.Delete("t", 1, 6), ErrTupleDeleted)
	mustOK(t, s.Abort(1))
	mustOK(t, s.Begin(3))
	mustOK(t, s.Delete("t", 3, 0))
}

func TestVisibleErrorPrecedence(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("t", 1, 0))
	snap := s.Snapshot()
	if _, err := s.Visible("t", 9, 0, snap); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("want ErrTxNotFound, got %v", err)
	}
	mustOK(t, s.Begin(2))
	mustOK(t, s.Abort(2))
	if _, err := s.Visible("t", 2, 0, snap); !errors.Is(err, ErrTxNotRunning) {
		t.Fatalf("want ErrTxNotRunning, got %v", err)
	}
	if _, err := s.Visible("t", 1, -1, snap); !errors.Is(err, ErrNegativeCommandID) {
		t.Fatalf("want ErrNegativeCommandID, got %v", err)
	}
	if _, err := s.Visible("t", 1, 0, snap+100); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("want ErrUnknownSnapshot, got %v", err)
	}
	if _, err := s.Visible("nope", 1, 0, snap); !errors.Is(err, ErrTupleNotFound) {
		t.Fatalf("want ErrTupleNotFound, got %v", err)
	}
}

func TestRejectedDeleteDoesNotAdvanceTreeMaxCmd(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("t", 1, 2))
	mustOK(t, s.Delete("t", 1, 3))
	mustErr(t, s.Delete("t", 1, 10), ErrTupleDeleted)
	mustOK(t, s.Insert("u", 1, 4))
}

func TestSnapshotIDsAreSequential(t *testing.T) {
	s := NewStore()
	for want := 1; want <= 5; want++ {
		if got := s.Snapshot(); got != want {
			t.Fatalf("Snapshot() = %d, want %d", got, want)
		}
	}
}
