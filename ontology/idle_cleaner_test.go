package ontology

import (
	"errors"
	"testing"
)

func TestRegistrationBoundaryAndExample(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 50, 2)

	touch := func(key string, now int64, wantTimer int64, wantCleaned []string) {
		t.Helper()
		gotCleaned, gotTimer, err := cleaner.Touch([]byte(key), now)
		if err != nil {
			t.Fatalf("Touch(%q, %d): unexpected error: %v", key, now, err)
		}
		if gotTimer != wantTimer {
			t.Fatalf("Touch(%q, %d): timer=%d, want %d", key, now, gotTimer, wantTimer)
		}
		assertStringSlices(t, gotCleaned, wantCleaned)
	}

	touch("a", 0, 30, nil)
	touch("a", 15, 30, nil)
	touch("a", 20, 30, nil)
	touch("a", 21, 50, nil)
	touch("a", 45, 50, nil)
	touch("b", 46, 76, nil)

	_, _, err := cleaner.Touch([]byte("c"), 47)
	if !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("Touch(c, 47): error=%v, want ErrCapacityLimit", err)
	}
	if cleaner.clock != 46 {
		t.Fatalf("clock after rejected touch=%d, want 46", cleaner.clock)
	}

	touch("c", 50, 80, []string{"a"})

	_, _, err = cleaner.Touch([]byte("a"), 50)
	if !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("Touch(a, 50): error=%v, want ErrCapacityLimit", err)
	}

	touch("b", 60, 76, nil)
	got, err := cleaner.Advance(76)
	if err != nil {
		t.Fatalf("Advance(76): unexpected error: %v", err)
	}
	assertStringSlices(t, got, []string{"b"})

	if cleaner.timerOps != 4 {
		t.Fatalf("timerOps=%d, want 4", cleaner.timerOps)
	}
	if cleaner.Cleaned() != 2 {
		t.Fatalf("cleaned=%d, want 2", cleaner.Cleaned())
	}
}

func TestConstructorValidation(t *testing.T) {
	validCases := []struct {
		name         string
		minRetention int64
		maxRetention int64
		maxLifetime  int64
		capacity     int
	}{
		{name: "minimum values", minRetention: 1, maxRetention: 1, maxLifetime: 1, capacity: 1},
		{name: "lifetime below max", minRetention: 10, maxRetention: 30, maxLifetime: 20, capacity: 2},
		{name: "upper bounds", minRetention: 1_000_000_000_000, maxRetention: 1_000_000_000_000, maxLifetime: 1_000_000_000_000, capacity: 1_000_000},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewIdleStateCleaner(tc.minRetention, tc.maxRetention, tc.maxLifetime, tc.capacity); err != nil {
				t.Fatalf("valid constructor: %v", err)
			}
		})
	}

	invalidCases := []struct {
		name         string
		minRetention int64
		maxRetention int64
		maxLifetime  int64
		capacity     int
	}{
		{name: "min zero", minRetention: 0, maxRetention: 1, maxLifetime: 1, capacity: 1},
		{name: "min negative", minRetention: -1, maxRetention: 1, maxLifetime: 1, capacity: 1},
		{name: "max below min", minRetention: 10, maxRetention: 9, maxLifetime: 10, capacity: 1},
		{name: "max too large", minRetention: 1, maxRetention: 1_000_000_000_001, maxLifetime: 1_000_000_000_001, capacity: 1},
		{name: "lifetime below min", minRetention: 10, maxRetention: 30, maxLifetime: 9, capacity: 1},
		{name: "lifetime too large", minRetention: 1, maxRetention: 1, maxLifetime: 1_000_000_000_001, capacity: 1},
		{name: "capacity zero", minRetention: 1, maxRetention: 1, maxLifetime: 1, capacity: 0},
		{name: "capacity too large", minRetention: 1, maxRetention: 1, maxLifetime: 1, capacity: 1_000_001},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewIdleStateCleaner(tc.minRetention, tc.maxRetention, tc.maxLifetime, tc.capacity)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error=%v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestRegistrationPeriodEqualsMaxMinusMinPlusOne(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 1_000_000_000_000, 1)

	cleaner.Touch([]byte("k"), 0)
	initialOps := cleaner.timerOps

	_, timer, err := cleaner.Touch([]byte("k"), 20)
	if err != nil || timer != 30 || cleaner.timerOps != initialOps {
		t.Fatalf("equal boundary: timer=%d ops=%d err=%v", timer, cleaner.timerOps, err)
	}

	_, timer, err = cleaner.Touch([]byte("k"), 21)
	if err != nil || timer != 51 || cleaner.timerOps != initialOps+1 {
		t.Fatalf("plus-one boundary: timer=%d ops=%d err=%v", timer, cleaner.timerOps, err)
	}

	const end int64 = 100_000
	formula := int64(end/21 + 1)
	heavy := mustNewCleaner(t, 10, 30, 1_000_000_000_000, 1)
	for now := int64(0); now <= end; now++ {
		if _, _, err := heavy.Touch([]byte("k"), now); err != nil {
			t.Fatalf("Touch at %d: %v", now, err)
		}
	}
	if heavy.timerOps != formula {
		t.Fatalf("timerOps=%d, want floor(%d/21)+1=%d", heavy.timerOps, end, formula)
	}
	if formula != 4762 {
		t.Fatalf("formula=%d, want 4762", formula)
	}
}

func TestExpiryBoundaryAndLifetimeCases(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 25, 1)

	_, timer, _ := cleaner.Touch([]byte("k"), 0)
	if timer != 25 {
		t.Fatalf("initial timer=%d, want 25", timer)
	}

	var got []string
	var err error
	for now := int64(1); now <= 24; now++ {
		got, timer, err = cleaner.Touch([]byte("k"), now)
		if err != nil || len(got) != 0 || timer != 25 {
			t.Fatalf("sustained touch at %d: got=%v timer=%d err=%v", now, got, timer, err)
		}
	}
	if cleaner.timerOps != 1 {
		t.Fatalf("timerOps before lifetime expiry=%d, want 1", cleaner.timerOps)
	}

	got, timer, err = cleaner.Touch([]byte("k"), 25)
	if err != nil || len(got) != 1 || got[0] != "k" || timer != 50 {
		t.Fatalf("touch at expiry: got=%v timer=%d err=%v", got, timer, err)
	}
	created := mustEntry(t, cleaner, "k").created
	if created != 25 {
		t.Fatalf("created=%d, want 25", created)
	}

	_, timer, _ = cleaner.Touch([]byte("k"), 26)
	if timer != 50 || cleaner.timerOps != 2 {
		t.Fatalf("lifetime clamp no-op: timer=%d ops=%d", timer, cleaner.timerOps)
	}

	got, err = cleaner.Advance(50)
	if err != nil || len(got) != 1 || got[0] != "k" {
		t.Fatalf("lifetime expiry: got=%v err=%v", got, err)
	}
}

func TestRejectedTouchAtomicityAndDueCapacityRelease(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 100, 2)
	cleaner.Touch([]byte("a"), 0)
	cleaner.Touch([]byte("b"), 1)

	_, _, err := cleaner.Touch([]byte("d"), 29)
	if !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("rejected full-capacity touch: %v", err)
	}
	if cleaner.clock != 1 || cleaner.Size() != 2 || cleaner.Cleaned() != 0 {
		t.Fatalf("rejected touch changed state: clock=%d size=%d cleaned=%d",
			cleaner.clock, cleaner.Size(), cleaner.Cleaned())
	}

	got, timer, err := cleaner.Touch([]byte("d"), 31)
	if err != nil || timer != 61 {
		t.Fatalf("touch after capacity release: timer=%d err=%v", timer, err)
	}
	assertStringSlices(t, got, []string{"a", "b"})
}

func TestAdvanceAtCurrentClock(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 100, 1)
	got, err := cleaner.Advance(0)
	if err != nil || len(got) != 0 {
		t.Fatalf("Advance at initial clock: got=%v err=%v", got, err)
	}

	cleaner.Touch([]byte("k"), 0)
	got, err = cleaner.Advance(0)
	if err != nil || len(got) != 0 {
		t.Fatalf("Advance at current clock: got=%v err=%v", got, err)
	}

	_, err = cleaner.Advance(-1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Advance(-1): error=%v, want ErrInvalidArgument", err)
	}

	_, err = cleaner.Advance(20)
	if err != nil {
		t.Fatalf("Advance(20): %v", err)
	}
	_, err = cleaner.Advance(29)
	// 29 is still forward; continue to a rollback.
	_, err = cleaner.Advance(19)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error=%v, want ErrClockRollback", err)
	}
}

func TestCapacityAccountingAndRejectionAtomicity(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 100, 2)
	cleaner.Touch([]byte("a"), 0)
	cleaner.Touch([]byte("b"), 0)

	_, _, err := cleaner.Touch([]byte("c"), 10)
	if !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("new key rejection error=%v", err)
	}
	if cleaner.clock != 0 || cleaner.Size() != 2 {
		t.Fatalf("state changed after rejected touch: clock=%d size=%d", cleaner.clock, cleaner.Size())
	}

	got, timer, err := cleaner.Touch([]byte("c"), 30)
	if err != nil || timer != 60 {
		t.Fatalf("expired-capacity touch: timer=%d err=%v", timer, err)
	}
	assertStringSlices(t, got, []string{"a", "b"})

	got, err = cleaner.Advance(50)
	if err != nil || len(got) != 0 {
		t.Fatalf("Advance one before expiry: got=%v err=%v", got, err)
	}

	got, timer, err = cleaner.Touch([]byte("c"), 60)
	if err != nil || timer != 90 {
		t.Fatalf("recreate after expiry: timer=%d err=%v", timer, err)
	}
	assertStringSlices(t, got, []string{"c"})
	if mustEntry(t, cleaner, "c").created != 60 {
		t.Fatalf("existing due key was not recreated")
	}
}

func TestAdvanceNoOpRollbackAndTieOrder(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 100, 4)
	cleaner.Touch([]byte("b"), 0)
	cleaner.Touch([]byte("a"), 0)
	cleaner.Touch([]byte("c"), 1)

	got, err := cleaner.Advance(30)
	if err != nil {
		t.Fatalf("Advance(30): unexpected error: %v", err)
	}
	assertStringSlices(t, got, []string{"a", "b"})

	_, err = cleaner.Advance(29)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error=%v, want ErrClockRollback", err)
	}
}

func TestErrorPrecedenceAndQueries(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 100, 1)
	cleaner.Touch([]byte("k"), 5)

	_, _, err := cleaner.Touch(nil, 4)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Touch invalid/rollback: %v", err)
	}
	_, _, err = cleaner.Touch([]byte("k"), 1_000_000_000_001)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Touch now too large: %v", err)
	}
	if cleaner.Has(nil) {
		t.Fatalf("Has(empty) = true")
	}
	if !cleaner.Has([]byte("k")) {
		t.Fatalf("Has(k) = false")
	}
	if timer, err := cleaner.Timer([]byte("k")); err != nil || timer != 35 {
		t.Fatalf("Timer(k)=%d,%v", timer, err)
	}
	if _, err := cleaner.Timer([]byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Timer(missing)=%v, want ErrNotFound", err)
	}
	if _, err := TimerEmpty(cleaner); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Timer(empty)=%v, want ErrInvalidArgument", err)
	}
}

func mustNewCleaner(t *testing.T, minRetention, maxRetention, maxLifetime int64, capacity int) *IdleStateCleaner {
	t.Helper()
	cleaner, err := NewIdleStateCleaner(minRetention, maxRetention, maxLifetime, capacity)
	if err != nil {
		t.Fatalf("NewIdleStateCleaner(%d,%d,%d,%d): %v", minRetention, maxRetention, maxLifetime, capacity, err)
	}
	return cleaner
}

func mustEntry(t *testing.T, cleaner *IdleStateCleaner, key string) *timerEntry {
	t.Helper()
	ent := cleaner.entries[key]
	if ent == nil {
		t.Fatalf("entry %q does not exist", key)
	}
	return ent
}

func assertStringSlices(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice=%v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice=%v, want %v", got, want)
		}
	}
}

func TimerEmpty(cleaner *IdleStateCleaner) (int64, error) {
	return cleaner.Timer(nil)
}
