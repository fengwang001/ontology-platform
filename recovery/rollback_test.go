package recovery_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/recovery"
)

// TestSavepointEqualToRecordLSN: a savepoint equal to a record's own LSN
// keeps that record; only later updates are undone.
func TestSavepointEqualToRecordLSN(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 10) // LSN 1
	mustUpdate(t, e, 1, 0, 20) // LSN 2
	mustUpdate(t, e, 1, 0, 30) // LSN 3
	n, err := e.Rollback(1, 2)
	if err != nil || n != 1 {
		t.Fatalf("Rollback(1,2) = %d, %v; want 1, nil", n, err)
	}
	checkLog(t, e, []recovery.Record{
		u(1, 1, 0, 10, 0),
		u(2, 1, 0, 20, 1),
		u(3, 1, 0, 30, 2),
		c(4, 1, 0, -30, 3, 2),
	})
	checkPage(t, e, 0, 30)
}

// TestRollbackToNextIsZeroRecords: rolling back to the current next
// pointer succeeds without appending anything.
func TestRollbackToNextIsZeroRecords(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 5) // LSN 1
	n, err := e.Rollback(1, 1)
	if err != nil || n != 0 {
		t.Fatalf("Rollback(1,1) = %d, %v; want 0, nil", n, err)
	}
	if got := e.LogLen(); got != 1 {
		t.Fatalf("log length = %d, want 1", got)
	}
	// Savepoint 0 on a transaction with no records: also a no-op.
	mustBegin(t, e, 2)
	n, err = e.Rollback(2, 0)
	if err != nil || n != 0 {
		t.Fatalf("Rollback(2,0) = %d, %v; want 0, nil", n, err)
	}
	// After a full rollback, next is 0; rolling back to 0 again is a no-op.
	if _, err := e.Rollback(1, 0); err != nil {
		t.Fatalf("Rollback(1,0): %v", err)
	}
	n, err = e.Rollback(1, 0)
	if err != nil || n != 0 {
		t.Fatalf("second Rollback(1,0) = %d, %v; want 0, nil", n, err)
	}
}

// TestPartialRollbackThenFullRollback: after a partial rollback the chain
// contains compensation records; continuing updates and then rolling back
// fully must skip the already-compensated region (jump over C records
// without writing new ones) and never undo an update twice.
func TestPartialRollbackThenFullRollback(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 10) // LSN 1
	mustUpdate(t, e, 1, 0, 20) // LSN 2
	mustUpdate(t, e, 1, 0, 40) // LSN 3
	n, err := e.Rollback(1, 1) // undo LSN 3 and 2
	if err != nil || n != 2 {
		t.Fatalf("Rollback(1,1) = %d, %v; want 2, nil", n, err)
	}
	mustUpdate(t, e, 1, 0, 7) // LSN 6, prev 5
	n, err = e.Rollback(1, 0)
	if err != nil || n != 2 {
		t.Fatalf("Rollback(1,0) = %d, %v; want 2, nil", n, err)
	}
	checkLog(t, e, []recovery.Record{
		u(1, 1, 0, 10, 0),
		u(2, 1, 0, 20, 1),
		u(3, 1, 0, 40, 2),
		c(4, 1, 0, -40, 3, 2),
		c(5, 1, 0, -20, 4, 1),
		u(6, 1, 0, 7, 5),
		c(7, 1, 0, -7, 6, 5),
		c(8, 1, 0, -10, 7, 0),
	})
	checkPage(t, e, 0, 0)
}

// TestNextPointsAtCompensation: when next(t) refers to a compensation
// record, a rollback only follows its undoNext without writing anything.
func TestNextPointsAtCompensation(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 3)                   // LSN 1
	mustUpdate(t, e, 1, 0, 5)                   // LSN 2
	if _, err := e.Rollback(1, 1); err != nil { // LSN 3: C undoing LSN 2
		t.Fatalf("Rollback(1,1): %v", err)
	}
	// next(1) is now 1 (undoNext of the C at LSN 3). Rolling back to 1
	// must append nothing.
	n, err := e.Rollback(1, 1)
	if err != nil || n != 0 {
		t.Fatalf("Rollback(1,1) = %d, %v; want 0, nil", n, err)
	}
	if got := e.LogLen(); got != 3 {
		t.Fatalf("log length = %d, want 3", got)
	}
	checkPage(t, e, 0, 3)
}

// TestTerminatedTransactionsRejectOperations.
func TestTerminatedTransactionsRejectOperations(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 1)
	if err := e.Commit(1); err != nil {
		t.Fatalf("Commit(1): %v", err)
	}
	mustBegin(t, e, 2)
	mustUpdate(t, e, 2, 0, 2)
	if _, err := e.Abort(2); err != nil {
		t.Fatalf("Abort(2): %v", err)
	}
	for _, id := range []int{1, 2} {
		if err := e.Begin(id); !errors.Is(err, recovery.ErrTxnExists) {
			t.Fatalf("Begin(%d) = %v, want ErrTxnExists", id, err)
		}
		if _, err := e.Update(id, 0, 1); !errors.Is(err, recovery.ErrTxnTerminated) {
			t.Fatalf("Update(%d) = %v, want ErrTxnTerminated", id, err)
		}
		if _, err := e.Save(id); !errors.Is(err, recovery.ErrTxnTerminated) {
			t.Fatalf("Save(%d) = %v, want ErrTxnTerminated", id, err)
		}
		if err := e.Commit(id); !errors.Is(err, recovery.ErrTxnTerminated) {
			t.Fatalf("Commit(%d) = %v, want ErrTxnTerminated", id, err)
		}
		if _, err := e.Rollback(id, 0); !errors.Is(err, recovery.ErrTxnTerminated) {
			t.Fatalf("Rollback(%d) = %v, want ErrTxnTerminated", id, err)
		}
		if _, err := e.Abort(id); !errors.Is(err, recovery.ErrTxnTerminated) {
			t.Fatalf("Abort(%d) = %v, want ErrTxnTerminated", id, err)
		}
	}
	if _, err := e.Update(99, 0, 1); !errors.Is(err, recovery.ErrTxnNotFound) {
		t.Fatalf("Update(99) = %v, want ErrTxnNotFound", err)
	}
}

// TestArgumentValidation: invalid arguments are reported before any
// system-state check (including the crash check).
func TestArgumentValidation(t *testing.T) {
	if _, err := recovery.NewEngine(0); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("NewEngine(0) = %v, want ErrInvalidArg", err)
	}
	if _, err := recovery.NewEngine(recovery.MaxLmax + 1); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("NewEngine(MaxLmax+1) = %v, want ErrInvalidArg", err)
	}
	e := mustEngine(t, 10)
	mustBegin(t, e, 1)
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	// Even while crashed, invalid arguments win over ErrCrashed.
	if err := e.Begin(0); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("Begin(0) while crashed = %v, want ErrInvalidArg", err)
	}
	if _, err := e.Update(0, 0, 1); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("Update(0,...) while crashed = %v, want ErrInvalidArg", err)
	}
	if _, err := e.Update(1, recovery.MaxPageID+1, 1); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("Update with bad page = %v, want ErrInvalidArg", err)
	}
	if _, err := e.Update(1, 0, 0); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("Update with d=0 = %v, want ErrInvalidArg", err)
	}
	if _, err := e.Update(1, 0, recovery.MaxDelta+1); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("Update with d too large = %v, want ErrInvalidArg", err)
	}
	if _, err := e.RestartStep(0); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("RestartStep(0) = %v, want ErrInvalidArg", err)
	}
	// Valid arguments while crashed report ErrCrashed.
	if err := e.Begin(2); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Begin(2) while crashed = %v, want ErrCrashed", err)
	}
	if _, err := e.Update(1, 0, 1); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Update while crashed = %v, want ErrCrashed", err)
	}
	if _, err := e.Save(1); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Save while crashed = %v, want ErrCrashed", err)
	}
	if err := e.Commit(1); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Commit while crashed = %v, want ErrCrashed", err)
	}
	if _, err := e.Rollback(1, 0); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Rollback while crashed = %v, want ErrCrashed", err)
	}
	if _, err := e.Abort(1); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Abort while crashed = %v, want ErrCrashed", err)
	}
	if err := e.Crash(); !errors.Is(err, recovery.ErrCrashed) {
		t.Fatalf("Crash while crashed = %v, want ErrCrashed", err)
	}
}

// TestRestartRequiresCrash: Restart and RestartStep on a running system
// report ErrNotCrashed, with argument validation taking precedence.
func TestRestartRequiresCrash(t *testing.T) {
	e := mustEngine(t, 10)
	if _, err := e.Restart(); !errors.Is(err, recovery.ErrNotCrashed) {
		t.Fatalf("Restart = %v, want ErrNotCrashed", err)
	}
	if _, err := e.RestartStep(1); !errors.Is(err, recovery.ErrNotCrashed) {
		t.Fatalf("RestartStep(1) = %v, want ErrNotCrashed", err)
	}
	if _, err := e.RestartStep(0); !errors.Is(err, recovery.ErrInvalidArg) {
		t.Fatalf("RestartStep(0) = %v, want ErrInvalidArg", err)
	}
}

// TestBadSavepoint: savepoints must be 0 or one of the transaction's own
// record LSNs.
func TestBadSavepoint(t *testing.T) {
	e := mustEngine(t, 10)
	mustBegin(t, e, 1)
	mustBegin(t, e, 2)
	mustUpdate(t, e, 1, 0, 1) // LSN 1
	mustUpdate(t, e, 2, 0, 1) // LSN 2
	if _, err := e.Rollback(1, 2); !errors.Is(err, recovery.ErrBadSavepoint) {
		t.Fatalf("Rollback(1,2) = %v, want ErrBadSavepoint", err)
	}
	if _, err := e.Rollback(1, -1); !errors.Is(err, recovery.ErrBadSavepoint) {
		t.Fatalf("Rollback(1,-1) = %v, want ErrBadSavepoint", err)
	}
	if _, err := e.Rollback(1, 99); !errors.Is(err, recovery.ErrBadSavepoint) {
		t.Fatalf("Rollback(1,99) = %v, want ErrBadSavepoint", err)
	}
}

func snapshot(t *testing.T, e *recovery.Engine) ([]recovery.Record, int64) {
	t.Helper()
	return e.Log(), e.Page(0)
}

// TestLogFullRejectsAtomically: Rollback and Abort that would exceed Lmax
// are rejected as a whole, leaving log, pages and transaction state
// untouched.
func TestLogFullRejectsAtomically(t *testing.T) {
	e := mustEngine(t, 4)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 10) // LSN 1
	mustUpdate(t, e, 1, 0, 20) // LSN 2
	mustUpdate(t, e, 1, 0, 40) // LSN 3, one slot left

	beforeLog, beforePage := snapshot(t, e)
	// Rollback to 0 needs 3 compensation records: rejected.
	if _, err := e.Rollback(1, 0); !errors.Is(err, recovery.ErrLogFull) {
		t.Fatalf("Rollback(1,0) = %v, want ErrLogFull", err)
	}
	// Abort needs 3 compensation records plus the end record: rejected.
	if _, err := e.Abort(1); !errors.Is(err, recovery.ErrLogFull) {
		t.Fatalf("Abort(1) = %v, want ErrLogFull", err)
	}
	// A single-record update still fits.
	if _, err := e.Update(1, 0, 80); err != nil {
		t.Fatalf("Update after rejections: %v", err)
	}
	// Now the log is completely full.
	if _, err := e.Update(1, 0, 1); !errors.Is(err, recovery.ErrLogFull) {
		t.Fatalf("Update on full log = %v, want ErrLogFull", err)
	}
	if err := e.Commit(1); !errors.Is(err, recovery.ErrLogFull) {
		t.Fatalf("Commit on full log = %v, want ErrLogFull", err)
	}
	afterLog, _ := snapshot(t, e)
	wantLog := append(append([]recovery.Record{}, beforeLog...), u(4, 1, 0, 80, 3))
	if len(afterLog) != len(wantLog) {
		t.Fatalf("log changed by rejected operations: %v", afterLog)
	}
	for i := range wantLog {
		if afterLog[i] != wantLog[i] {
			t.Fatalf("log changed by rejected operations: %v", afterLog)
		}
	}
	checkPage(t, e, 0, beforePage+80)
	// The transaction is still active after all the rejections.
	s, err := e.Save(1)
	if err != nil || s != 4 {
		t.Fatalf("Save(1) = %d, %v; want 4, nil", s, err)
	}
}

// TestRestartIgnoresLmax: the restart undo phase appends past Lmax.
func TestRestartIgnoresLmax(t *testing.T) {
	e := mustEngine(t, 3)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 10)
	mustUpdate(t, e, 1, 0, 20)
	mustUpdate(t, e, 1, 0, 40) // log full
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	n, err := e.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if n != 4 {
		t.Fatalf("Restart appended %d, want 4", n)
	}
	checkLog(t, e, []recovery.Record{
		u(1, 1, 0, 10, 0),
		u(2, 1, 0, 20, 1),
		u(3, 1, 0, 40, 2),
		c(4, 1, 0, -40, 3, 2),
		c(5, 1, 0, -20, 4, 1),
		c(6, 1, 0, -10, 5, 0),
		e2(7, 1, 6),
	})
	checkPage(t, e, 0, 0)
}

// TestRestartEmptyLosersFirst: losers whose next is already 0 at crash
// time receive their end records first, in ascending transaction order.
func TestRestartEmptyLosersFirst(t *testing.T) {
	e := mustEngine(t, 100)
	mustBegin(t, e, 3) // no records at all
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 5)                   // LSN 1
	if _, err := e.Rollback(1, 0); err != nil { // LSN 2: C, next(1) = 0
		t.Fatalf("Rollback(1,0): %v", err)
	}
	mustBegin(t, e, 2)
	mustUpdate(t, e, 2, 0, 7) // LSN 3
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	n, err := e.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if n != 4 {
		t.Fatalf("Restart appended %d, want 4", n)
	}
	checkLog(t, e, []recovery.Record{
		u(1, 1, 0, 5, 0),
		c(2, 1, 0, -5, 1, 0),
		u(3, 2, 0, 7, 0),
		e2(4, 1, 2), // loser 1: next already 0, ascending id first
		e2(5, 3, 0), // loser 3: no records
		c(6, 2, 0, -7, 3, 0),
		e2(7, 2, 6),
	})
	checkPage(t, e, 0, 0)
}

// TestRestartNoLosers: a crash with no active transactions restarts with
// zero appended records.
func TestRestartNoLosers(t *testing.T) {
	e := mustEngine(t, 10)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 5)
	if err := e.Commit(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	n, err := e.Restart()
	if err != nil || n != 0 {
		t.Fatalf("Restart = %d, %v; want 0, nil", n, err)
	}
	if e.Crashed() {
		t.Fatal("system still crashed after Restart")
	}
	checkPage(t, e, 0, 5) // committed update survives
	// Normal operations resume.
	mustBegin(t, e, 2)
	mustUpdate(t, e, 2, 0, 7)
	checkPage(t, e, 0, 12)
}

// TestRejectedOperationsDoNotChangeState exercises a series of rejected
// operations and verifies the log and pages afterwards.
func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	e := mustEngine(t, 10)
	mustBegin(t, e, 1)
	mustUpdate(t, e, 1, 0, 5)
	before := e.Log()
	rejects := []func() error{
		func() error { return e.Begin(1) },
		func() error { return e.Begin(0) },
		func() error { _, err := e.Update(2, 0, 1); return err },
		func() error { _, err := e.Update(1, -1, 1); return err },
		func() error { _, err := e.Save(7); return err },
		func() error { _, err := e.Rollback(1, 5); return err },
		func() error { _, err := e.Restart(); return err },
		func() error { _, err := e.RestartStep(3); return err },
	}
	for i, op := range rejects {
		if err := op(); err == nil {
			t.Fatalf("rejected op %d unexpectedly succeeded", i)
		}
	}
	after := e.Log()
	if len(after) != len(before) {
		t.Fatalf("log changed: %v -> %v", before, after)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("log changed: %v -> %v", before, after)
		}
	}
	checkPage(t, e, 0, 5)
}

// TestConcurrentInvariant hammers the engine from many goroutines and
// verifies the serializability invariant: every page value equals the sum
// of the deltas of all U and C records in the log.
func TestConcurrentInvariant(t *testing.T) {
	e := mustEngine(t, recovery.MaxLmax)
	const workers = 8
	const ops = 250
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			id := base + 1
			if err := e.Begin(id); err != nil {
				t.Errorf("Begin(%d): %v", id, err)
				return
			}
			for i := 0; i < ops; i++ {
				switch i % 5 {
				case 0:
					if _, err := e.Update(id, i%7, int64(i+1)); err != nil {
						t.Errorf("Update: %v", err)
						return
					}
				case 1:
					if _, err := e.Save(id); err != nil {
						t.Errorf("Save: %v", err)
						return
					}
				case 2:
					if _, err := e.Rollback(id, 0); err != nil {
						t.Errorf("Rollback: %v", err)
						return
					}
				default:
					if _, err := e.Update(id, i%7, -int64(i+1)); err != nil {
						t.Errorf("Update: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	sums := map[int]int64{}
	for _, r := range e.Log() {
		if r.Type == recovery.RecUpdate || r.Type == recovery.RecCompensation {
			sums[r.Page] += r.Delta
		}
	}
	for p, want := range sums {
		if got := e.Page(p); got != want {
			t.Fatalf("page %d = %d, want %d (sum of log deltas)", p, got, want)
		}
	}
}
