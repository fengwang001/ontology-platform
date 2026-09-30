package checkpoint

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		Tasks:                 2,
		MaxConcurrent:         3,
		MinInterval:           0,
		Timeout:               10,
		MaxConsecutiveTimeout: 5,
		Retention:             2,
	}
}

func newTestCoordinator(cfg Config) (*Coordinator, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := log.New(buf, "", 0)
	c, err := NewCoordinator(cfg, logger)
	if err != nil {
		panic(err)
	}
	return c, buf
}

func mustTrigger(t *testing.T, c *Coordinator) uint64 {
	t.Helper()
	id, err := c.Trigger()
	if err != nil {
		t.Fatalf("Trigger() unexpected error: %v", err)
	}
	return id
}

func mustConfirm(t *testing.T, c *Coordinator, id uint64, task int) {
	t.Helper()
	if err := c.Confirm(id, task); err != nil {
		t.Fatalf("Confirm(id=%d, task=%d) unexpected error: %v", id, task, err)
	}
}

func mustAdvance(t *testing.T, c *Coordinator, clock int64) {
	t.Helper()
	if err := c.Advance(clock); err != nil {
		t.Fatalf("Advance(%d) unexpected error: %v", clock, err)
	}
}

func mustComplete(t *testing.T, c *Coordinator, id uint64, tasks int) {
	t.Helper()
	for task := 0; task < tasks; task++ {
		mustConfirm(t, c, id, task)
	}
	cp, ok := c.Get(id)
	if !ok {
		t.Fatalf("Get(%d) not found", id)
	}
	if cp.State != StateCompleted {
		t.Fatalf("checkpoint %d state = %s, want completed", id, cp.State)
	}
}

func requireReject(t *testing.T, err error, reason Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection with reason %s, got nil error", reason)
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error (%v)", err, err)
	}
	if ce.Reason != reason {
		t.Fatalf("reason = %s, want %s (err=%v)", ce.Reason, reason, err)
	}
}

func TestSubsumption(t *testing.T) {
	c, _ := newTestCoordinator(testConfig())
	id1 := mustTrigger(t, c)
	id2 := mustTrigger(t, c)
	mustConfirm(t, c, id1, 0)
	mustComplete(t, c, id2, 2)

	cp1, _ := c.Get(id1)
	if cp1.State != StateAborted || cp1.AbortReason != AbortSubsumed {
		t.Fatalf("cp1 = (%s, %s), want (aborted, subsumed)", cp1.State, cp1.AbortReason)
	}
	if got := c.Snapshot().ConsecutiveTimeouts; got != 0 {
		t.Fatalf("consecutive timeouts = %d, want 0", got)
	}
	requireReject(t, c.Confirm(id1, 1), ReasonAlreadyEnded)
	if got := c.Snapshot().Retained; !reflect.DeepEqual(got, []uint64{id2}) {
		t.Fatalf("retained = %v, want [%d]", got, id2)
	}
}

func TestTimeoutCascadeToFailure(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 1
	cfg.MaxConcurrent = 5
	cfg.MaxConsecutiveTimeout = 1
	c, _ := newTestCoordinator(cfg)

	id1 := mustTrigger(t, c)
	id2 := mustTrigger(t, c)
	id3 := mustTrigger(t, c)

	// One advance times out id1 (count=1) then id2 (count=2 > 1):
	// the coordinator fails and id3 is aborted with reason "failed"
	// without being counted.
	mustAdvance(t, c, 10)

	snap := c.Snapshot()
	if !snap.Failed {
		t.Fatal("coordinator should be failed")
	}
	if snap.ConsecutiveTimeouts != 2 {
		t.Fatalf("consecutive timeouts = %d, want 2", snap.ConsecutiveTimeouts)
	}
	if len(snap.InProgress) != 0 {
		t.Fatalf("in-progress = %v, want empty", snap.InProgress)
	}
	for id, want := range map[uint64]AbortReason{id1: AbortTimeout, id2: AbortTimeout, id3: AbortFailed} {
		cp, _ := c.Get(id)
		if cp.State != StateAborted || cp.AbortReason != want {
			t.Fatalf("cp%d = (%s, %s), want (aborted, %s)", id, cp.State, cp.AbortReason, want)
		}
	}

	// A failed coordinator rejects every trigger without consuming ids.
	nextID := c.Snapshot().NextID
	_, err := c.Trigger()
	requireReject(t, err, ReasonCoordinatorFailed)
	if got := c.Snapshot().NextID; got != nextID {
		t.Fatalf("rejected trigger consumed an id: nextID %d -> %d", nextID, got)
	}

	// Restore with an empty retained set is rejected and keeps the failure.
	_, err = c.Restore()
	requireReject(t, err, ReasonNoCheckpoint)
	if !c.Snapshot().Failed {
		t.Fatal("rejected restore must not change failed state")
	}
}

func TestConfirmAroundTimeoutBoundary(t *testing.T) {
	c, _ := newTestCoordinator(testConfig())

	// Completed just before the timeout advance: survives the advance.
	id1 := mustTrigger(t, c)
	mustAdvance(t, c, 9)
	mustComplete(t, c, id1, 2)
	mustAdvance(t, c, 10)
	if cp, _ := c.Get(id1); cp.State != StateCompleted {
		t.Fatalf("cp1 state = %s, want completed", cp.State)
	}

	// Timed out by the advance: a later confirm reports already_ended.
	id2 := mustTrigger(t, c)
	mustAdvance(t, c, 20)
	if cp, _ := c.Get(id2); cp.State != StateAborted || cp.AbortReason != AbortTimeout {
		t.Fatalf("cp2 = (%s, %s), want (aborted, timeout)", cp.State, cp.AbortReason)
	}
	requireReject(t, c.Confirm(id2, 0), ReasonAlreadyEnded)

	// Exactly at started_at + timeout the checkpoint is aborted.
	id3 := mustTrigger(t, c) // started at clock 20
	mustAdvance(t, c, 30)
	if cp, _ := c.Get(id3); cp.State != StateAborted || cp.AbortReason != AbortTimeout {
		t.Fatalf("cp3 = (%s, %s), want (aborted, timeout)", cp.State, cp.AbortReason)
	}
}

func TestIntervalFromCompletion(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 1
	cfg.MinInterval = 5
	cfg.Timeout = 1000
	c, _ := newTestCoordinator(cfg)

	// No completion yet: interval does not limit the first trigger.
	id1 := mustTrigger(t, c)
	mustComplete(t, c, id1, 1) // completed at clock 0

	// Interval is measured from the completion clock.
	_, err := c.Trigger()
	requireReject(t, err, ReasonIntervalTooShort)
	mustAdvance(t, c, 4)
	_, err = c.Trigger()
	requireReject(t, err, ReasonIntervalTooShort)
	mustAdvance(t, c, 5)
	if id := mustTrigger(t, c); id != 2 {
		t.Fatalf("id = %d, want 2 (rejected triggers must not consume ids)", id)
	}
}

func TestRetentionAndRestoreIDContinuation(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 1
	cfg.MaxConcurrent = 5
	cfg.Timeout = 1000
	c, _ := newTestCoordinator(cfg)

	// Complete 1, 2, 3: only the largest R=2 are retained.
	for want := uint64(1); want <= 3; want++ {
		id := mustTrigger(t, c)
		if id != want {
			t.Fatalf("id = %d, want %d", id, want)
		}
		mustComplete(t, c, id, 1)
	}
	if got := c.Snapshot().Retained; !reflect.DeepEqual(got, []uint64{2, 3}) {
		t.Fatalf("retained = %v, want [2 3]", got)
	}

	// Restore picks the largest retained id and aborts in-progress ones.
	inFlight := mustTrigger(t, c) // id 4, in progress
	got, err := c.Restore()
	if err != nil {
		t.Fatalf("Restore() unexpected error: %v", err)
	}
	if got != 3 {
		t.Fatalf("restore id = %d, want 3", got)
	}
	if cp, _ := c.Get(inFlight); cp.State != StateAborted || cp.AbortReason != AbortRestored {
		t.Fatalf("cp4 = (%s, %s), want (aborted, restored)", cp.State, cp.AbortReason)
	}

	// Ids keep increasing after restore; nothing is reused.
	if id := mustTrigger(t, c); id != 5 {
		t.Fatalf("id after restore = %d, want 5", id)
	}
}

func TestRestoreRecoversFailedCoordinator(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 1
	cfg.MaxConsecutiveTimeout = 0
	c, _ := newTestCoordinator(cfg)

	id1 := mustTrigger(t, c)
	mustComplete(t, c, id1, 1) // retained so restore has a target

	mustTrigger(t, c)
	mustAdvance(t, c, 10) // the in-flight one times out: count=1 > 0 -> failed
	if !c.Snapshot().Failed {
		t.Fatal("coordinator should be failed")
	}

	got, err := c.Restore()
	if err != nil {
		t.Fatalf("Restore() unexpected error: %v", err)
	}
	if got != id1 {
		t.Fatalf("restore id = %d, want %d", got, id1)
	}
	snap := c.Snapshot()
	if snap.Failed || snap.ConsecutiveTimeouts != 0 {
		t.Fatalf("after restore: failed=%v count=%d, want false/0", snap.Failed, snap.ConsecutiveTimeouts)
	}
	if id := mustTrigger(t, c); id != 3 {
		t.Fatalf("id after recovery = %d, want 3", id)
	}
}

func TestTriggerRejectionPriority(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 1
	cfg.MaxConcurrent = 1
	cfg.MinInterval = 5
	cfg.MaxConsecutiveTimeout = 0
	cfg.Timeout = 10
	c, _ := newTestCoordinator(cfg)

	id1 := mustTrigger(t, c)
	mustComplete(t, c, id1, 1) // completed at clock 0

	// Concurrency full and interval too short both apply: concurrency
	// is reported first.
	mustAdvance(t, c, 5)     // interval satisfied: 5 - 0 >= 5
	id2 := mustTrigger(t, c) // fills the single slot
	_ = id2
	_, err := c.Trigger()
	requireReject(t, err, ReasonConcurrencyFull)

	// Timeout the in-flight one to fail the coordinator (count=1 > 0).
	mustAdvance(t, c, 15) // clock 15 >= started_at 5 + timeout 10
	if !c.Snapshot().Failed {
		t.Fatal("coordinator should be failed")
	}
	// Failed beats every other trigger rejection.
	_, err = c.Trigger()
	requireReject(t, err, ReasonCoordinatorFailed)
}

func TestConfirmRejectionPriority(t *testing.T) {
	c, _ := newTestCoordinator(testConfig())
	id1 := mustTrigger(t, c)
	mustComplete(t, c, id1, 2)
	id2 := mustTrigger(t, c)
	mustConfirm(t, c, id2, 0)

	// Task out of range beats not-found.
	requireReject(t, c.Confirm(9999, 7), ReasonTaskOutOfRange)
	// Not found beats already-ended/duplicate.
	requireReject(t, c.Confirm(9999, 0), ReasonNotFound)
	// Already ended beats duplicate (id1 task 0 confirmed before completion).
	requireReject(t, c.Confirm(id1, 0), ReasonAlreadyEnded)
	// Duplicate confirm on an in-progress checkpoint.
	requireReject(t, c.Confirm(id2, 0), ReasonDuplicateConfirm)

	// Rejections changed nothing.
	snap := c.Snapshot()
	if snap.NextID != 3 || len(snap.InProgress) != 1 {
		t.Fatalf("state changed by rejected confirms: %+v", snap)
	}
}

func TestClockBackwardRejected(t *testing.T) {
	c, _ := newTestCoordinator(testConfig())
	mustAdvance(t, c, 5)
	requireReject(t, c.Advance(4), ReasonClockBackward)
	if got := c.Snapshot().Clock; got != 5 {
		t.Fatalf("clock = %d, want 5 (rejected advance must not move the clock)", got)
	}
	mustAdvance(t, c, 5) // staying in place is allowed
}

func TestConcurrentDuplicateConfirmSucceedsOnce(t *testing.T) {
	cfg := testConfig()
	c, err := NewCoordinator(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	id := mustTrigger(t, c)

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Confirm(id, 0)
		}()
	}
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		ce, ok := err.(*Error)
		if !ok || ce.Reason != ReasonDuplicateConfirm {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("same-task concurrent confirms succeeded %d times, want exactly 1", succeeded)
	}
}

func TestConcurrentTriggerRespectsLimitAndContiguousIDs(t *testing.T) {
	cfg := testConfig()
	cfg.MaxConcurrent = 3
	c, err := NewCoordinator(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ids []uint64
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := c.Trigger()
			if err == nil {
				mu.Lock()
				ids = append(ids, id)
				mu.Unlock()
				return
			}
			ce, ok := err.(*Error)
			if !ok || ce.Reason != ReasonConcurrencyFull {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(ids) != cfg.MaxConcurrent {
		t.Fatalf("accepted %d triggers, want %d", len(ids), cfg.MaxConcurrent)
	}
	seen := map[uint64]bool{}
	for _, id := range ids {
		if id < 1 || id > uint64(cfg.MaxConcurrent) || seen[id] {
			t.Fatalf("ids not contiguous from 1 without holes: %v", ids)
		}
		seen[id] = true
	}
	if got := len(c.Snapshot().InProgress); got != cfg.MaxConcurrent {
		t.Fatalf("in-progress = %d, want %d", got, cfg.MaxConcurrent)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	cfg := testConfig()
	cfg.Tasks = 3
	cfg.MaxConcurrent = 4
	cfg.MaxConsecutiveTimeout = 100
	c, err := NewCoordinator(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id, err := c.Trigger()
				if err == nil {
					_ = c.Confirm(id, (worker+i)%cfg.Tasks)
				}
				_ = c.Advance(int64(i))
				_, _ = c.Restore()
				_ = c.Snapshot()
			}
		}(worker)
	}
	wg.Wait()

	snap := c.Snapshot()
	if len(snap.InProgress) > cfg.MaxConcurrent {
		t.Fatalf("in-progress = %d exceeds limit %d", len(snap.InProgress), cfg.MaxConcurrent)
	}
	if len(snap.Retained) > cfg.Retention {
		t.Fatalf("retained = %d exceeds R=%d", len(snap.Retained), cfg.Retention)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() ([]string, Snapshot) {
		cfg := testConfig()
		cfg.Tasks = 2
		cfg.MaxConsecutiveTimeout = 1
		c, err := NewCoordinator(cfg, log.New(io.Discard, "", 0))
		if err != nil {
			t.Fatal(err)
		}
		var results []string
		record := func(format string, args ...any) {
			results = append(results, fmt.Sprintf(format, args...))
		}
		id1, err := c.Trigger()
		record("trigger -> %d, %v", id1, err)
		id2, err := c.Trigger()
		record("trigger -> %d, %v", id2, err)
		record("confirm -> %v", c.Confirm(id2, 0))
		record("confirm -> %v", c.Confirm(id2, 1))
		record("advance -> %v", c.Advance(5))
		id3, err := c.Trigger()
		record("trigger -> %d, %v", id3, err)
		record("advance -> %v", c.Advance(20))
		restored, err := c.Restore()
		record("restore -> %d, %v", restored, err)
		id4, err := c.Trigger()
		record("trigger -> %d, %v", id4, err)
		return results, c.Snapshot()
	}

	results1, snap1 := run()
	results2, snap2 := run()
	if !reflect.DeepEqual(results1, results2) {
		t.Fatalf("replayed op results differ:\n%v\n%v", results1, results2)
	}
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("replayed snapshots differ:\n%+v\n%+v", snap1, snap2)
	}
}

func TestLogsContainInputOutputAndBasis(t *testing.T) {
	c, buf := newTestCoordinator(testConfig())
	id := mustTrigger(t, c)
	mustComplete(t, c, id, 2)
	mustAdvance(t, c, 3)
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{"op=trigger", "op=confirm", "op=advance", "op=restore", "in=", "out=", "basis="} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
