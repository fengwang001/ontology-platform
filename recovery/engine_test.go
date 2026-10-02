package recovery_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/recovery"
)

func mustEngine(t *testing.T, lmax int) *recovery.Engine {
	t.Helper()
	e, err := recovery.NewEngine(lmax)
	if err != nil {
		t.Fatalf("NewEngine(%d): %v", lmax, err)
	}
	return e
}

func mustBegin(t *testing.T, e *recovery.Engine, id int) {
	t.Helper()
	if err := e.Begin(id); err != nil {
		t.Fatalf("Begin(%d): %v", id, err)
	}
}

func mustUpdate(t *testing.T, e *recovery.Engine, id, page int, d int64) int {
	t.Helper()
	lsn, err := e.Update(id, page, d)
	if err != nil {
		t.Fatalf("Update(%d,%d,%d): %v", id, page, d, err)
	}
	return lsn
}

func checkLog(t *testing.T, e *recovery.Engine, want []recovery.Record) {
	t.Helper()
	got := e.Log()
	if len(got) != len(want) {
		t.Fatalf("log length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v\ngot:  %v\nwant: %v", i+1, got[i], want[i], got, want)
		}
	}
}

func checkPage(t *testing.T, e *recovery.Engine, page int, want int64) {
	t.Helper()
	if got := e.Page(page); got != want {
		t.Fatalf("page %d = %d, want %d", page, got, want)
	}
}

func rec(lsn int, typ recovery.RecType, txn, page int, delta int64, prev, undoNext int) recovery.Record {
	return recovery.Record{LSN: lsn, Type: typ, Txn: txn, Page: page, Delta: delta, PrevLSN: prev, UndoNext: undoNext}
}

func u(lsn, txn, page int, delta int64, prev int) recovery.Record {
	return rec(lsn, recovery.RecUpdate, txn, page, delta, prev, 0)
}

func c(lsn, txn, page int, delta int64, prev, undoNext int) recovery.Record {
	return rec(lsn, recovery.RecCompensation, txn, page, delta, prev, undoNext)
}

func e2(lsn, txn, prev int) recovery.Record {
	return rec(lsn, recovery.RecEnd, txn, 0, 0, prev, 0)
}

// specExample replays the scenario from the problem statement on a fresh
// engine and crashes before returning.
func specExample(t *testing.T, e *recovery.Engine) {
	t.Helper()
	mustBegin(t, e, 1)
	mustBegin(t, e, 2)
	if lsn := mustUpdate(t, e, 1, 5, 10); lsn != 1 {
		t.Fatalf("Update(1,5,10) = LSN %d, want 1", lsn)
	}
	if lsn := mustUpdate(t, e, 2, 5, 3); lsn != 2 {
		t.Fatalf("Update(2,5,3) = LSN %d, want 2", lsn)
	}
	if lsn := mustUpdate(t, e, 1, 6, 7); lsn != 3 {
		t.Fatalf("Update(1,6,7) = LSN %d, want 3", lsn)
	}
	s, err := e.Save(1)
	if err != nil || s != 3 {
		t.Fatalf("Save(1) = %d, %v; want 3, nil", s, err)
	}
	if lsn := mustUpdate(t, e, 1, 5, 1); lsn != 4 {
		t.Fatalf("Update(1,5,1) = LSN %d, want 4", lsn)
	}
	n, err := e.Rollback(1, 3)
	if err != nil || n != 1 {
		t.Fatalf("Rollback(1,3) = %d, %v; want 1, nil", n, err)
	}
	if lsn := mustUpdate(t, e, 1, 6, 2); lsn != 6 {
		t.Fatalf("Update(1,6,2) = LSN %d, want 6", lsn)
	}
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
}

var specExampleLog = []recovery.Record{
	u(1, 1, 5, 10, 0),
	u(2, 2, 5, 3, 0),
	u(3, 1, 6, 7, 1),
	u(4, 1, 5, 1, 3),
	c(5, 1, 5, -1, 4, 3),
	u(6, 1, 6, 2, 5),
	c(7, 1, 6, -2, 6, 5),
	c(8, 1, 6, -7, 7, 1),
	c(9, 2, 5, -3, 2, 0),
	e2(10, 2, 9),
	c(11, 1, 5, -10, 8, 0),
	e2(12, 1, 11),
}

func TestSpecExampleRestart(t *testing.T) {
	e := mustEngine(t, 100)
	specExample(t, e)
	n, err := e.Restart()
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if n != 6 {
		t.Fatalf("Restart appended %d records, want 6", n)
	}
	checkLog(t, e, specExampleLog)
	checkPage(t, e, 5, 0)
	checkPage(t, e, 6, 0)
	if e.Crashed() {
		t.Fatal("system still crashed after Restart")
	}
}

// TestSpecExampleRestartStepSplits verifies that interrupting the undo
// phase at every possible position (step size n, then a final Restart)
// yields exactly the same log as a single Restart.
func TestSpecExampleRestartStepSplits(t *testing.T) {
	for n := 1; n <= len(specExampleLog); n++ {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			e := mustEngine(t, 100)
			specExample(t, e)
			got, err := e.RestartStep(n)
			if err != nil {
				t.Fatalf("RestartStep(%d): %v", n, err)
			}
			wantFirst := n
			if wantFirst > 6 {
				wantFirst = 6
			}
			if got != wantFirst {
				t.Fatalf("RestartStep(%d) appended %d, want %d", n, got, wantFirst)
			}
			rest, err := e.Restart()
			if errors.Is(err, recovery.ErrNotCrashed) {
				rest = 0
			} else if err != nil {
				t.Fatalf("Restart: %v", err)
			}
			if got+rest != 6 {
				t.Fatalf("total appended = %d, want 6", got+rest)
			}
			checkLog(t, e, specExampleLog)
			checkPage(t, e, 5, 0)
			checkPage(t, e, 6, 0)
		})
	}
}

// TestSpecExampleRestartStepOneByOne steps through the undo phase one
// record at a time.
func TestSpecExampleRestartStepOneByOne(t *testing.T) {
	e := mustEngine(t, 100)
	specExample(t, e)
	total := 0
	for {
		n, err := e.RestartStep(1)
		if err != nil {
			t.Fatalf("RestartStep(1): %v", err)
		}
		if n == 0 {
			break
		}
		total += n
	}
	if total != 6 {
		t.Fatalf("stepped restart appended %d, want 6", total)
	}
	checkLog(t, e, specExampleLog)
	if _, err := e.RestartStep(1); !errors.Is(err, recovery.ErrNotCrashed) {
		t.Fatalf("RestartStep after completion = %v, want ErrNotCrashed", err)
	}
}
