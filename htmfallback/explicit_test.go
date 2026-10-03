package htmfallback

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, n, s, w, a, r, sk, f int) *Controller {
	t.Helper()
	controller, err := New(n, s, w, a, r, sk, f)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return controller
}

func mustLock(t *testing.T, controller *Controller, thread int) LockResult {
	t.Helper()
	result, err := controller.Lock(thread)
	if err != nil {
		t.Fatalf("Lock(%d) returned error: %v", thread, err)
	}
	return result
}

func mustAccess(t *testing.T, controller *Controller, thread, address int, isWrite bool) AccessResult {
	t.Helper()
	result, err := controller.Access(thread, address, isWrite)
	if err != nil {
		t.Fatalf("Access(%d,%d,%v) returned error: %v", thread, address, isWrite, err)
	}
	return result
}

func mustUnlock(t *testing.T, controller *Controller, thread int) UnlockResult {
	t.Helper()
	result, err := controller.Unlock(thread)
	if err != nil {
		t.Fatalf("Unlock(%d) returned error: %v", thread, err)
	}
	return result
}

func capacityAbort(t *testing.T, controller *Controller, thread int) {
	t.Helper()
	mustLock(t, controller, thread)
	mustAccess(t, controller, thread, 0, false)
	result := mustAccess(t, controller, thread, 1, false)
	if result.Reason != ReasonCapacity {
		t.Fatalf("setup capacity = %+v", result)
	}
}

func TestReadAfterWriteSameAddressCountsOnce(t *testing.T) {
	controller := mustNew(t, 1, 1, 1, 1, 0, 0, 1)
	mustLock(t, controller, 0)
	mustAccess(t, controller, 0, 0, true)
	result := mustAccess(t, controller, 0, 0, false)
	if result.State != StateSpeculative {
		t.Fatalf("read-after-write aborted: %+v", result)
	}
	snapshot := controller.Snapshot()
	if !reflect.DeepEqual(snapshot.Threads[0].ReadSet, []int{0}) ||
		!reflect.DeepEqual(snapshot.Threads[0].WriteSet, []int{0}) {
		t.Fatalf("sets = read%v write%v", snapshot.Threads[0].ReadSet, snapshot.Threads[0].WriteSet)
	}
}

func TestCapacityBoundary(t *testing.T) {
	controller := mustNew(t, 1, 1, 2, 3, 0, 0, 1)
	mustLock(t, controller, 0)
	mustAccess(t, controller, 0, 0, false)
	mustAccess(t, controller, 0, 1, true)
	result := mustAccess(t, controller, 0, 2, false)
	if result.State != StateAborted || result.Reason != ReasonCapacity {
		t.Fatalf("third distinct address = %+v", result)
	}
}

func TestWriteCapacityAccessStillAbortsConflictsFirst(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 3, 8, 0, 1)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 1, 2, true)
	mustAccess(t, controller, 0, 1, false)

	result := mustAccess(t, controller, 0, 2, true)
	if !reflect.DeepEqual(result.Aborted, []int{1}) {
		t.Fatalf("conflict victims = %v", result.Aborted)
	}
	if result.State != StateAborted || result.Reason != ReasonCapacity {
		t.Fatalf("writer result = %+v", result)
	}
	snapshot := controller.Snapshot()
	if snapshot.Threads[1].Reason != ReasonConflict || snapshot.Threads[1].Retries != 1 {
		t.Fatalf("victim = %+v", snapshot.Threads[1])
	}
}

func TestConcurrentCallsAreRaceFree(t *testing.T) {
	controller := mustNew(t, 4, 2, 2, 8, 2, 2, 3)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			thread := worker % 4
			_, _ = controller.Lock(thread)
			_, _ = controller.Access(thread, (worker+thread)%8, worker%2 == 0)
			_, _ = controller.Unlock(thread)
			_ = controller.Snapshot()
		}(worker)
	}
	wait.Wait()
}

func TestWaitingDoesNotConsumeSkipOrRetries(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 2, 0, 8, 2)
	capacityAbort(t, controller, 0)
	firstFallback := mustLock(t, controller, 0)
	if firstFallback.Skip != 8 || firstFallback.ConsecutiveFallbacks != 1 {
		t.Fatalf("initial fallback = %+v", firstFallback)
	}

	wait := mustLock(t, controller, 1)
	if !wait.Waited || wait.State != StateIdle || wait.Skip != 8 {
		t.Fatalf("wait = %+v", wait)
	}
}

func TestCapacityFallbackDoesNotDecrementSkip(t *testing.T) {
	controller := mustNew(t, 1, 1, 1, 2, 0, 8, 2)
	capacityAbort(t, controller, 0)
	result := mustLock(t, controller, 0)
	if result.State != StateFallback || result.Reason != ReasonCapacity ||
		result.Skip != 8 || result.ConsecutiveFallbacks != 1 {
		t.Fatalf("capacity fallback = %+v", result)
	}
}

func TestRetryBoundary(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 1, 1, 0, 1)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 0, 0, true)
	first := mustAccess(t, controller, 1, 0, false)
	if first.State != StateSpeculative || !reflect.DeepEqual(first.Aborted, []int{0}) {
		t.Fatalf("first conflict = %+v", first)
	}
	if snapshot := controller.Snapshot(); snapshot.Threads[0].Retries != 1 {
		t.Fatalf("victim retries = %d", snapshot.Threads[0].Retries)
	}

	atBudget := mustLock(t, controller, 0)
	if atBudget.State != StateSpeculative || atBudget.Reason != ReasonNone {
		t.Fatalf("r==R lock = %+v", atBudget)
	}
	mustAccess(t, controller, 0, 0, false)

	second := mustAccess(t, controller, 1, 0, true)
	if second.State != StateSpeculative || !reflect.DeepEqual(second.Aborted, []int{0}) {
		t.Fatalf("second conflict = %+v", second)
	}
	if snapshot := controller.Snapshot(); snapshot.Threads[0].Retries != 2 {
		t.Fatalf("victim retries = %d", snapshot.Threads[0].Retries)
	}
	overBudgetVictim := mustLock(t, controller, 0)
	if overBudgetVictim.State != StateFallback || overBudgetVictim.Reason != ReasonConflict {
		t.Fatalf("victim r>R lock = %+v", overBudgetVictim)
	}
}

func TestRetryZeroFallbackAfterFirstConflict(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 1, 0, 0, 1)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 0, 0, true)
	conflict := mustAccess(t, controller, 1, 0, false)
	if !reflect.DeepEqual(conflict.Aborted, []int{0}) {
		t.Fatalf("conflict = %+v", conflict)
	}
	if snapshot := controller.Snapshot(); snapshot.Threads[0].Retries != 1 {
		t.Fatalf("victim retries = %d", snapshot.Threads[0].Retries)
	}
	fallback := mustLock(t, controller, 0)
	if fallback.State != StateFallback || fallback.Reason != ReasonConflict {
		t.Fatalf("R=0 fallback = %+v", fallback)
	}
}

func TestSkipZeroLockOrder(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 1, 0, 0, 1)
	first := mustLock(t, controller, 0)
	second := mustLock(t, controller, 1)
	if first.State != StateSpeculative || second.State != StateSpeculative {
		t.Fatalf("locks = %+v,%+v", first, second)
	}
}

func TestCommitAndFallbackUnlockResetOnlySpecifiedCounts(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 1, 0, 0, 2)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 0, 0, true)
	conflict := mustAccess(t, controller, 1, 0, false)
	if !reflect.DeepEqual(conflict.Aborted, []int{0}) {
		t.Fatalf("conflict = %+v", conflict)
	}
	if snapshot := controller.Snapshot(); snapshot.Threads[0].Retries != 1 {
		t.Fatalf("victim retries = %d", snapshot.Threads[0].Retries)
	}

	commit := mustUnlock(t, controller, 1)
	if commit.Retries != 0 || commit.ConsecutiveFallbacks != 0 {
		t.Fatalf("speculative commit = %+v", commit)
	}

	overBudget := mustLock(t, controller, 0)
	if overBudget.State != StateFallback || overBudget.ConsecutiveFallbacks != 1 {
		t.Fatalf("fallback = %+v", overBudget)
	}
	unlock := mustUnlock(t, controller, 0)
	if unlock.Retries != 0 || unlock.ConsecutiveFallbacks != 1 {
		t.Fatalf("fallback unlock = %+v", unlock)
	}
}

func TestFallbackThresholdAndSkipPathDoNotCount(t *testing.T) {
	controller := mustNew(t, 2, 1, 4, 2, 1, 2, 2)

	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 0, 0, true)
	mustAccess(t, controller, 1, 0, true)
	mustLock(t, controller, 0)
	mustAccess(t, controller, 0, 1, true)
	mustAccess(t, controller, 1, 1, true)
	first := mustLock(t, controller, 0)
	if first.ConsecutiveFallbacks != 1 || first.Skip != 0 {
		t.Fatalf("first counted fallback = %+v", first)
	}
	mustUnlock(t, controller, 0)

	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 0, 0, true)
	mustAccess(t, controller, 1, 0, true)
	mustLock(t, controller, 0)
	mustAccess(t, controller, 0, 1, true)
	mustAccess(t, controller, 1, 1, true)
	second := mustLock(t, controller, 0)
	if second.ConsecutiveFallbacks != 0 || second.Skip != 2 {
		t.Fatalf("threshold fallback = %+v", second)
	}
	mustUnlock(t, controller, 0)

	firstSkipPath := mustLock(t, controller, 0)
	if firstSkipPath.State != StateFallback || firstSkipPath.Skip != 1 || firstSkipPath.ConsecutiveFallbacks != 0 {
		t.Fatalf("first skip path = %+v", firstSkipPath)
	}
	mustUnlock(t, controller, 0)

	secondSkipPath := mustLock(t, controller, 0)
	if secondSkipPath.State != StateFallback || secondSkipPath.Skip != 0 || secondSkipPath.ConsecutiveFallbacks != 0 {
		t.Fatalf("second skip path = %+v", secondSkipPath)
	}
}

func TestFallbackAccessDoesNotChangeAbortedSpeculator(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 3, 8, 0, 1)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 1, 0, true)
	mustAccess(t, controller, 0, 1, false)
	capacity := mustAccess(t, controller, 0, 2, true)
	if capacity.Reason != ReasonCapacity {
		t.Fatalf("capacity setup = %+v", capacity)
	}

	fallback := mustLock(t, controller, 0)
	if !reflect.DeepEqual(fallback.Aborted, []int{1}) {
		t.Fatalf("fallback = %+v", fallback)
	}
	mustAccess(t, controller, 0, 0, true)
	snapshot := controller.Snapshot()
	if snapshot.Threads[1].State != StateAborted ||
		snapshot.Threads[1].Reason != ReasonLockHeld ||
		snapshot.Threads[1].Retries != 0 {
		t.Fatalf("fallback access changed thread: %+v", snapshot.Threads[1])
	}
}

func TestLockHeldAbortRelocks(t *testing.T) {
	controller := mustNew(t, 2, 1, 1, 3, 8, 0, 1)
	mustLock(t, controller, 0)
	mustLock(t, controller, 1)
	mustAccess(t, controller, 1, 0, true)
	mustAccess(t, controller, 0, 1, false)
	capacity := mustAccess(t, controller, 0, 2, true)
	if capacity.Reason != ReasonCapacity {
		t.Fatalf("capacity setup = %+v", capacity)
	}
	fallback := mustLock(t, controller, 0)
	if !reflect.DeepEqual(fallback.Aborted, []int{1}) {
		t.Fatalf("aborted speculators = %+v", fallback)
	}
	mustUnlock(t, controller, 0)

	relock := mustLock(t, controller, 1)
	if relock.State != StateSpeculative {
		t.Fatalf("relock after lock-held abort = %+v", relock)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	invalid := [][7]int{
		{0, 1, 1, 1, 0, 0, 1}, {17, 1, 1, 1, 0, 0, 1},
		{1, 0, 1, 1, 0, 0, 1}, {1, 9, 1, 1, 0, 0, 1},
		{1, 1, 0, 1, 0, 0, 1}, {1, 1, 5, 1, 0, 0, 1},
		{1, 1, 1, 0, 0, 0, 1}, {1, 1, 1, 65, 0, 0, 1},
		{1, 1, 1, 1, -1, 0, 1}, {1, 1, 1, 1, 9, 0, 1},
		{1, 1, 1, 1, 0, -1, 1}, {1, 1, 1, 1, 0, 9, 1},
		{1, 1, 1, 1, 0, 0, 0}, {1, 1, 1, 1, 0, 0, 9},
	}
	for _, values := range invalid {
		_, err := New(values[0], values[1], values[2], values[3], values[4], values[5], values[6])
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%v) error = %v", values, err)
		}
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	controller := mustNew(t, 1, 1, 1, 1, 0, 0, 1)

	if _, err := controller.Lock(1); !errors.Is(err, ErrInvalidThread) {
		t.Fatalf("Lock thread error = %v", err)
	}
	if _, err := controller.Access(1, 0, false); !errors.Is(err, ErrInvalidThread) {
		t.Fatalf("Access thread before address error = %v", err)
	}
	if _, err := controller.Unlock(1); !errors.Is(err, ErrInvalidThread) {
		t.Fatalf("Unlock thread error = %v", err)
	}

	if _, err := controller.Access(0, 0, false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Access idle error = %v", err)
	}
	if _, err := controller.Access(0, 1, false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Access state before address error = %v", err)
	}
	if _, err := controller.Unlock(0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Unlock idle error = %v", err)
	}

	mustLock(t, controller, 0)
	before := controller.Snapshot()
	if _, err := controller.Access(0, 1, false); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("Access address error = %v", err)
	}
	if !reflect.DeepEqual(controller.Snapshot(), before) {
		t.Fatalf("rejected call changed state: before=%+v after=%+v", before, controller.Snapshot())
	}
}

func TestSpecExample(t *testing.T) {
	controller := mustNew(t, 2, 2, 1, 3, 1, 2, 2)
	mustLock(t, controller, 0)
	mustAccess(t, controller, 0, 0, true)
	mustLock(t, controller, 1)
	conflict := mustAccess(t, controller, 1, 0, false)
	if !reflect.DeepEqual(conflict.Aborted, []int{0}) || conflict.State != StateSpeculative {
		t.Fatalf("conflict = %+v", conflict)
	}
	capacity := mustAccess(t, controller, 1, 2, false)
	if capacity.State != StateAborted || capacity.Reason != ReasonCapacity || capacity.Skip != 2 {
		t.Fatalf("capacity = %+v", capacity)
	}

	firstSkip := mustLock(t, controller, 0)
	if firstSkip.State != StateFallback || firstSkip.Skip != 1 {
		t.Fatalf("first skip fallback = %+v", firstSkip)
	}
	wait := mustLock(t, controller, 1)
	if !wait.Waited {
		t.Fatalf("wait = %+v", wait)
	}
	mustUnlock(t, controller, 0)

	capacityFallback := mustLock(t, controller, 1)
	if capacityFallback.Skip != 1 || capacityFallback.ConsecutiveFallbacks != 1 {
		t.Fatalf("capacity fallback = %+v", capacityFallback)
	}
	mustUnlock(t, controller, 1)

	lastSkip := mustLock(t, controller, 0)
	if lastSkip.State != StateFallback || lastSkip.Skip != 0 || lastSkip.ConsecutiveFallbacks != 1 {
		t.Fatalf("last skip fallback = %+v", lastSkip)
	}
	mustUnlock(t, controller, 0)

	speculative := mustLock(t, controller, 0)
	if speculative.State != StateSpeculative {
		t.Fatalf("speculation resumed = %+v", speculative)
	}
}
