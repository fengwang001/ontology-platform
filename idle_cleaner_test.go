package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestRetainWindowBoundary(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 1000, 4)

	touchAt := func(now int64, wantTimer int64, wantOps int64) {
		t.Helper()
		cleaned, timer, err := cleaner.Touch("k", now)
		if err != nil {
			t.Fatalf("Touch(k, %d): %v; input=k output=%v reason=access", now, err, cleaned)
		}
		if len(cleaned) != 0 || timer != wantTimer || cleaner.timerOps != wantOps {
			t.Fatalf("Touch(k, %d): cleaned=%v timer=%d ops=%d, want timer=%d ops=%d; reason=now+Min vs current", now, cleaned, timer, cleaner.timerOps, wantTimer, wantOps)
		}
	}

	touchAt(0, 30, 1)
	touchAt(20, 30, 1)
	touchAt(21, 51, 2)
	touchAt(41, 51, 2)
	touchAt(42, 72, 3)
}

func TestTimerBoundaryAndAbsoluteLifetime(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 50, 4)

	mustTouch(t, cleaner, "a", 0, 30, 1)
	mustTouch(t, cleaner, "a", 21, 50, 2)
	mustTouch(t, cleaner, "a", 45, 50, 2)

	cleaned, timer, err := cleaner.Touch("b", 46)
	if err != nil || len(cleaned) != 0 || timer != 76 || cleaner.timerOps != 3 {
		t.Fatalf("Touch(b,46)=%v,%d,%v ops=%d; reason=new key before due", cleaned, timer, err, cleaner.timerOps)
	}

	cleaned, err = cleaner.Advance(49)
	if err != nil || len(cleaned) != 0 {
		t.Fatalf("Advance(49)=%v,%v; reason=timer 50 is one millisecond away", cleaned, err)
	}

	cleaned, timer, err = cleaner.Touch("c", 50)
	if err != nil || len(cleaned) != 1 || cleaned[0] != "a" || timer != 80 {
		t.Fatalf("Touch(c,50)=%v,%d,%v; reason=timer equal to now fires before new access", cleaned, timer, err)
	}
	created := cleaner.items["c"].created
	if created != 50 {
		t.Fatalf("created after due-key access=%d, want 50", created)
	}
}

func TestLifetimeExpiryRecreatesPersistentlyTouchedKey(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 50, 2)

	mustTouch(t, cleaner, "k", 0, 30, 1)
	mustTouch(t, cleaner, "k", 21, 50, 2)
	mustTouch(t, cleaner, "k", 45, 50, 2)

	cleaned, timer, err := cleaner.Touch("k", 50)
	if err != nil || len(cleaned) != 1 || cleaned[0] != "k" || timer != 80 {
		t.Fatalf("Touch(k,50)=%v,%d,%v; reason=created+Hmax expires then key is recreated", cleaned, timer, err)
	}
	entry := cleaner.items["k"]
	if entry.created != 50 {
		t.Fatalf("recreated entry created=%d, want 50", entry.created)
	}
	if cleaner.Cleaned() != 1 || cleaner.timerOps != 3 {
		t.Fatalf("cleaned=%d ops=%d; reason=cleanup plus new registration", cleaner.Cleaned(), cleaner.timerOps)
	}
}

func TestCapacityCountsDueKeysAsReleased(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 50, 2)

	mustTouch(t, cleaner, "a", 0, 30, 1)
	mustTouch(t, cleaner, "a", 21, 50, 2)
	mustTouch(t, cleaner, "b", 46, 76, 3)

	_, _, err := cleaner.Touch("c", 47)
	if !errors.Is(err, ErrCapacityExceeded) || cleaner.Size() != 2 || cleaner.Cleaned() != 0 {
		t.Fatalf("Touch(c,47) err=%v size=%d cleaned=%d; reason=3 live entries exceed K=2", err, cleaner.Size(), cleaner.Cleaned())
	}
	if clock := cleaner.now; clock != 46 {
		t.Fatalf("clock after rejected Touch=%d, want 46; reason=no cleanup or clock advance", clock)
	}

	cleaned, timer, err := cleaner.Touch("c", 50)
	if err != nil || len(cleaned) != 1 || cleaned[0] != "a" || timer != 80 {
		t.Fatalf("Touch(c,50)=%v,%d,%v; reason=due key releases one slot before check", cleaned, timer, err)
	}
	if cleaner.Size() != 2 || cleaner.Cleaned() != 1 {
		t.Fatalf("size=%d cleaned=%d after replacement", cleaner.Size(), cleaner.Cleaned())
	}

	_, _, err = cleaner.Touch("a", 50)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("Touch(a,50) err=%v; reason=no due entries and size=2", err)
	}
}

func TestSameTimerCleanupUsesKeyOrder(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 10, 100, 8)

	for _, key := range []string{"c", "a", "b"} {
		if _, _, err := cleaner.Touch(key, 0); err != nil {
			t.Fatalf("Touch(%s,0): %v", key, err)
		}
	}

	cleaned, err := cleaner.Advance(10)
	if err != nil || len(cleaned) != 3 || cleaned[0] != "a" || cleaned[1] != "b" || cleaned[2] != "c" {
		t.Fatalf("Advance(10)=%v,%v; reason=same timer sorted by bytewise key", cleaned, err)
	}
}

func TestAdvanceEqualOldTimeAndRollback(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 100, 2)
	mustTouch(t, cleaner, "a", 5, 35, 1)

	cleaned, err := cleaner.Advance(5)
	if err != nil || len(cleaned) != 0 || cleaner.now != 5 {
		t.Fatalf("Advance(5)=%v,%v now=%d; reason=equal clock is an empty operation", cleaned, err, cleaner.now)
	}

	_, err = cleaner.Advance(4)
	if !errors.Is(err, ErrClockRolledBack) || cleaner.now != 5 {
		t.Fatalf("Advance(4)=%v now=%d; reason=rollback rejected without state change", err, cleaner.now)
	}
}

func TestErrorPrecedenceAndQueries(t *testing.T) {
	cleaner := newTestCleaner(t, 10, 30, 100, 1)
	mustTouch(t, cleaner, "a", 10, 40, 1)

	_, _, err := cleaner.Touch("", 5)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key with rolled-back now err=%v; reason=invalid argument precedes rollback", err)
	}
	_, _, err = cleaner.Touch("b", 5)
	if !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("Touch(b,5) err=%v; reason=rollback precedes capacity", err)
	}
	_, _, err = cleaner.Touch("b", 10)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("Touch(b,10) err=%v; reason=a is live and K=1", err)
	}

	if has := cleaner.Has("missing"); has {
		t.Fatalf("Has(missing)=true")
	}
	if _, err := cleaner.Timer("missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Timer(missing) err=%v; reason=nonexistent key", err)
	}
	if timer, err := cleaner.Timer("a"); err != nil || timer != 40 {
		t.Fatalf("Timer(a)=%d,%v", timer, err)
	}
}

func TestContinuousAccessTimerOpsFormula(t *testing.T) {
	const maxT int64 = 100_000
	cleaner := newTestCleaner(t, 10, 30, 1_000_000_000_000, 1)

	var opsBefore int64
	for now := int64(0); now <= maxT; now++ {
		cleaned, _, err := cleaner.Touch("k", now)
		if err != nil {
			t.Fatalf("Touch(k,%d): %v", now, err)
		}
		if len(cleaned) != 0 {
			t.Fatalf("continuous key cleaned at %d", now)
		}

		wantOps := now/(30-10+1) + 1
		if cleaner.timerOps != wantOps {
			t.Fatalf("after now=%d ops=%d want=%d; reason=floor(now/(Max-Min+1))+1", now, cleaner.timerOps, wantOps)
		}
		if now == 0 {
			opsBefore = cleaner.timerOps
		}
	}
	if opsBefore != 1 || cleaner.timerOps != 4762 {
		t.Fatalf("ops start=%d final=%d want 1 and 4762", opsBefore, cleaner.timerOps)
	}
}

func TestConcurrentOperationsAppearSerialized(t *testing.T) {
	cleaner := newTestCleaner(t, 1, 3, 100, 128)
	var wait sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for now := int64(1); now <= 100; now++ {
				key := string(rune('a' + worker))
				_, _, _ = cleaner.Touch(key, now)
				_ = cleaner.Size()
				_ = cleaner.Cleaned()
			}
		}(worker)
	}
	wait.Wait()

	if cleaner.Size() > cleaner.capacity {
		t.Fatalf("size=%d exceeds K=%d", cleaner.Size(), cleaner.capacity)
	}
	for timer := cleaner.timers.PeekTimer(); timer != nil && timer.timer <= cleaner.now; timer = cleaner.timers.PeekTimer() {
		t.Fatalf("entry %q timer=%d remains at T=%d", timer.key, timer.timer, cleaner.now)
	}
}

func newTestCleaner(t *testing.T, min, max, hmax int64, capacity int) *IdleCleaner {
	t.Helper()
	cleaner, err := NewIdleCleaner(min, max, hmax, capacity)
	if err != nil {
		t.Fatalf("NewIdleCleaner(%d,%d,%d,%d): %v", min, max, hmax, capacity, err)
	}
	return cleaner
}

func mustTouch(t *testing.T, cleaner *IdleCleaner, key string, now, wantTimer, wantOps int64) {
	t.Helper()
	cleaned, timer, err := cleaner.Touch(key, now)
	if err != nil {
		t.Fatalf("Touch(%s,%d)=%v; input=%s output=%v reason=access", key, now, err, key, cleaned)
	}
	if len(cleaned) != 0 || timer != wantTimer || cleaner.timerOps != wantOps {
		t.Fatalf("Touch(%s,%d): cleaned=%v timer=%d ops=%d, want timer=%d ops=%d", key, now, cleaned, timer, cleaner.timerOps, wantTimer, wantOps)
	}
}
