package checkpoint

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	// Inputs, outputs and the decision basis are logged by the coordinator;
	// writing to stdout makes them visible under `go test -v`.
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func newForTest(t *testing.T, cfg Config) *Coordinator {
	t.Helper()
	cfg.Logger = testLogger()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func mustTrigger(t *testing.T, c *Coordinator) uint64 {
	t.Helper()
	id, err := c.Trigger()
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	return id
}

func assertErrIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// TestAbsorption verifies that a completion aborts every smaller inflight
// checkpoint as swallowed, keeps larger inflight ones, and resets the streak.
func TestAbsorption(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 2, MaxInflight: 4, MinInterval: 0, Timeout: time.Hour,
		ToleratedTimeouts: 2, Retention: 3,
	})
	cp1 := mustTrigger(t, c)
	// cp1 alone reaches its deadline: exactly one timeout, streak at 1.
	if err := c.Advance(60 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := c.Query().ConsecutiveTimeouts; got != 1 {
		t.Fatalf("consecutive timeouts = %d, want 1", got)
	}
	cp2 := mustTrigger(t, c)
	cp3 := mustTrigger(t, c)
	cp4 := mustTrigger(t, c)

	if err := c.Ack(cp3, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(cp3, 1); err != nil {
		t.Fatal(err)
	}

	snap := c.Query()
	for id, wantReason := range map[uint64]AbortReason{cp1: AbortTimeout, cp2: AbortSwallowed} {
		view := snap.Checkpoints[id]
		if view.Status != StatusAborted || view.AbortReason != wantReason {
			t.Fatalf("checkpoint %d = status %d reason %d, want aborted/%d", id, view.Status, view.AbortReason, wantReason)
		}
	}
	if view := snap.Checkpoints[cp3]; view.Status != StatusCompleted {
		t.Fatalf("checkpoint %d not completed", cp3)
	}
	if view := snap.Checkpoints[cp4]; view.Status != StatusInflight {
		t.Fatalf("larger checkpoint %d must stay inflight, got %d", cp4, view.Status)
	}
	if snap.ConsecutiveTimeouts != 0 {
		t.Fatalf("completion must clear streak, got %d", snap.ConsecutiveTimeouts)
	}
	if len(snap.InflightIDs) != 1 || snap.InflightIDs[0] != cp4 {
		t.Fatalf("inflight = %v, want [%d]", snap.InflightIDs, cp4)
	}
}

// TestMultipleTimeoutsInOneAdvance verifies ascending one-by-one timeout
// handling within one advance and failure when the streak exceeds tolerance;
// failure-driven aborts must not count toward the streak.
func TestMultipleTimeoutsInOneAdvance(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 1, MaxInflight: 5, Timeout: 10,
		ToleratedTimeouts: 2, Retention: 1,
	})
	cp1 := mustTrigger(t, c)
	if err := c.Advance(10); err != nil { // exactly at the boundary: due
		t.Fatal(err)
	}
	if snap := c.Query(); snap.ConsecutiveTimeouts != 1 || snap.Failed {
		t.Fatalf("after first timeout: streak %d failed %v", snap.ConsecutiveTimeouts, snap.Failed)
	}
	cp2 := mustTrigger(t, c)
	cp3 := mustTrigger(t, c)
	cp4 := mustTrigger(t, c)
	// cp2 -> streak 2, cp3 -> streak 3 > 2, failure; cp4 aborts as failed.
	if err := c.Advance(20); err != nil {
		t.Fatal(err)
	}
	snap := c.Query()
	if !snap.Failed {
		t.Fatal("coordinator must be failed")
	}
	if snap.ConsecutiveTimeouts != 3 {
		t.Fatalf("streak = %d, want 3 (cp4 failed-abort not counted)", snap.ConsecutiveTimeouts)
	}
	for id, wantReason := range map[uint64]AbortReason{
		cp1: AbortTimeout, cp2: AbortTimeout, cp3: AbortTimeout, cp4: AbortFailed,
	} {
		view := snap.Checkpoints[id]
		if view.Status != StatusAborted || view.AbortReason != wantReason {
			t.Fatalf("cp %d = %d/%d, want aborted/%d", id, view.Status, view.AbortReason, wantReason)
		}
	}
	// While failed, every trigger is rejected without consuming an id.
	_, err := c.Trigger()
	assertErrIs(t, err, ErrFailed)
	if got := c.Query().NextID; got != cp4+1 {
		t.Fatalf("next id = %d, want %d (rejected trigger consumes no id)", got, cp4+1)
	}
}

// TestAckAroundTimeout verifies clock >= triggered+timeout semantics and that
// an ack completing just before the boundary wins over the timeout.
func TestAckAroundTimeout(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 2, MaxInflight: 2, Timeout: 100,
		ToleratedTimeouts: 5, Retention: 2,
	})
	cp1 := mustTrigger(t, c)

	if err := c.Advance(99); err != nil { // one tick before boundary
		t.Fatal(err)
	}
	if err := c.Ack(cp1, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(cp1, 1); err != nil {
		t.Fatal(err)
	}
	if view := c.Query().Checkpoints[cp1]; view.Status != StatusCompleted {
		t.Fatalf("cp1 status = %d, want completed", view.Status)
	}

	// A checkpoint that reaches exactly triggered+timeout aborts on advance.
	cp2 := mustTrigger(t, c)
	if err := c.Advance(199); err != nil { // cp2 triggered at 99, deadline 199
		t.Fatal(err)
	}
	view := c.Query().Checkpoints[cp2]
	if view.Status != StatusAborted || view.AbortReason != AbortTimeout {
		t.Fatalf("cp2 = %d/%d, want aborted/timeout", view.Status, view.AbortReason)
	}
	assertErrIs(t, c.Ack(cp2, 0), ErrCheckpointEnded)
}

// TestMinIntervalFromCompletion verifies the interval is measured from the
// most recent completion clock; swallowed aborts must not move the anchor.
func TestMinIntervalFromCompletion(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 1, MaxInflight: 3, MinInterval: 50,
		Timeout: time.Hour, ToleratedTimeouts: 5, Retention: 2,
	})
	cp1 := mustTrigger(t, c) // no completion yet: interval not enforced
	if err := c.Advance(40); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(cp1, 0); err != nil { // completes at clock 40
		t.Fatal(err)
	}
	if _, err := c.Trigger(); !errors.Is(err, ErrIntervalTooShort) {
		t.Fatalf("trigger at 40: %v, want ErrIntervalTooShort", err)
	}
	if err := c.Advance(89); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Trigger(); !errors.Is(err, ErrIntervalTooShort) {
		t.Fatalf("trigger at 89: %v, want ErrIntervalTooShort", err)
	}
	if err := c.Advance(90); err != nil { // 90-40 == 50: exactly allowed
		t.Fatal(err)
	}
	cp2 := mustTrigger(t, c)

	// cp3 completes at 100 and swallows cp2; the swallow does not anchor time.
	if err := c.Advance(100); err != nil {
		t.Fatal(err)
	}
	cp3 := mustTrigger(t, c)
	if err := c.Ack(cp3, 0); err != nil {
		t.Fatal(err)
	}
	if view := c.Query().Checkpoints[cp2]; view.Status != StatusAborted || view.AbortReason != AbortSwallowed {
		t.Fatalf("cp2 = %d/%d, want aborted/swallowed", view.Status, view.AbortReason)
	}
	if err := c.Advance(149); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Trigger(); !errors.Is(err, ErrIntervalTooShort) {
		t.Fatalf("interval must anchor at completion 100, got %v", err)
	}
	if err := c.Advance(150); err != nil {
		t.Fatal(err)
	}
	mustTrigger(t, c)
}

// TestRetentionEvictionAndRecovery verifies only the largest R completed
// checkpoints stay recoverable, recovery returns the largest id, aborts
// inflight without counting, clears failure, and ids continue increasing.
func TestRetentionEvictionAndRecovery(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 1, MaxInflight: 6, Timeout: 10,
		ToleratedTimeouts: 1, Retention: 2,
	})
	var completed []uint64
	for range 4 {
		id := mustTrigger(t, c)
		if err := c.Ack(id, 0); err != nil {
			t.Fatal(err)
		}
		completed = append(completed, id)
	}
	snap := c.Query()
	if got, want := snap.RetainedIDs, []uint64{completed[2], completed[3]}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("retained = %v, want %v", got, want)
	}

	// Drive the coordinator to failure via consecutive timeouts.
	cp5 := mustTrigger(t, c)
	cp6 := mustTrigger(t, c)
	if err := c.Advance(10); err != nil {
		t.Fatal(err)
	}
	if !c.Query().Failed {
		t.Fatal("expected failure after two consecutive timeouts (tolerance 1)")
	}
	if view := c.Query().Checkpoints[cp6]; view.AbortReason != AbortTimeout {
		t.Fatalf("cp6 reason = %d, want timeout (the triggering timeout still counts)", view.AbortReason)
	}

	recID, err := c.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if recID != completed[3] {
		t.Fatalf("recover id = %d, want %d", recID, completed[3])
	}
	snap = c.Query()
	if snap.Failed || snap.ConsecutiveTimeouts != 0 {
		t.Fatalf("after recovery: failed %v streak %d", snap.Failed, snap.ConsecutiveTimeouts)
	}
	if len(snap.InflightIDs) != 0 {
		t.Fatalf("inflight after recovery = %v, want empty", snap.InflightIDs)
	}
	if view := snap.Checkpoints[cp5]; view.AbortReason != AbortTimeout {
		t.Fatalf("cp5 reason = %d, want timeout", view.AbortReason)
	}
	if got, want := snap.RetainedIDs, []uint64{completed[2], completed[3]}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("retained after recovery = %v, want %v", got, want)
	}
	cp7 := mustTrigger(t, c)
	if cp7 != cp6+1 {
		t.Fatalf("new id after recovery = %d, want %d", cp7, cp6+1)
	}

	// Recovery with an empty retention set is rejected and changes nothing.
	empty := newForTest(t, Config{Tasks: 1, MaxInflight: 1, Timeout: 10, Retention: 1})
	before := fingerprint(empty.Query())
	_, err = empty.Recover()
	assertErrIs(t, err, ErrNoCheckpoint)
	if fingerprint(empty.Query()) != before {
		t.Fatal("failed recovery must not change state")
	}
}

// TestRejectionPriorities verifies each rejection reason in the documented
// fixed order; rejected operations leave the state untouched.
func TestRejectionPriorities(t *testing.T) {
	c := newForTest(t, Config{
		Tasks: 2, MaxInflight: 1, MinInterval: 100,
		Timeout: time.Hour, ToleratedTimeouts: 0, Retention: 1,
	})
	cp1 := mustTrigger(t, c)

	before := fingerprint(c.Query())
	assertErrIs(t, c.Advance(-1), ErrClockBackward)
	if fingerprint(c.Query()) != before {
		t.Fatal("backward advance changed state")
	}

	// Ack priority 1: task out of range wins even over an unknown id.
	assertErrIs(t, c.Ack(9999, 5), ErrTaskOutOfRange)
	assertErrIs(t, c.Ack(cp1, -1), ErrTaskOutOfRange)
	// Ack priority 2: unknown id with in-range task.
	assertErrIs(t, c.Ack(9999, 0), ErrUnknownCheckpoint)
	// Trigger: concurrency full beats interval.
	_, err := c.Trigger()
	assertErrIs(t, err, ErrConcurrencyFull)

	// Complete cp1 at clock 0; trigger now fails on interval instead.
	if err := c.Ack(cp1, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(cp1, 1); err != nil {
		t.Fatal(err)
	}
	_, err = c.Trigger()
	assertErrIs(t, err, ErrIntervalTooShort)

	// Ack priority 3 vs 4: ended beats duplicate; duplicate for inflight.
	assertErrIs(t, c.Ack(cp1, 0), ErrCheckpointEnded)
	cp2 := mustTriggerAfter(t, c, 100)
	if err := c.Ack(cp2, 0); err != nil {
		t.Fatal(err)
	}
	assertErrIs(t, c.Ack(cp2, 0), ErrDuplicateAck)

	// Failure outranks interval and concurrency on trigger.
	if err := c.Advance(100 + time.Hour); err != nil {
		t.Fatal(err)
	}
	_, err = c.Trigger()
	assertErrIs(t, err, ErrFailed)
}

func mustTriggerAfter(t *testing.T, c *Coordinator, clock time.Duration) uint64 {
	t.Helper()
	if err := c.Advance(clock); err != nil {
		t.Fatal(err)
	}
	return mustTrigger(t, c)
}

// TestConcurrentAckExactlyOnce hammers one checkpoint with concurrent acks
// from each task: every task ack succeeds exactly once and the checkpoint
// completes exactly once.
func TestConcurrentAckExactlyOnce(t *testing.T) {
	const tasks = 4
	c := newForTest(t, Config{
		Tasks: tasks, MaxInflight: 1, Timeout: time.Hour,
		ToleratedTimeouts: 5, Retention: 1,
	})
	cp := mustTrigger(t, c)

	var wg sync.WaitGroup
	var successMu sync.Mutex
	successes := make(map[int]int)
	for task := range tasks {
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := c.Ack(cp, task)
				successMu.Lock()
				defer successMu.Unlock()
				switch {
				case err == nil:
					successes[task]++
				case errors.Is(err, ErrDuplicateAck):
				case errors.Is(err, ErrCheckpointEnded):
				default:
					t.Errorf("unexpected ack error: %v", err)
				}
			}()
		}
	}
	wg.Wait()

	if len(successes) != tasks {
		t.Fatalf("successful ack tasks = %v, want all %d", successes, tasks)
	}
	for task, count := range successes {
		if count != 1 {
			t.Fatalf("task %d succeeded %d times, want exactly 1", task, count)
		}
	}
	view := c.Query().Checkpoints[cp]
	if view.Status != StatusCompleted {
		t.Fatalf("checkpoint status = %d, want completed", view.Status)
	}
	if c.Query().ConsecutiveTimeouts != 0 {
		t.Fatal("completion under concurrency must clear streak")
	}
}

// TestConcurrentTriggersRespectLimit fires many triggers concurrently and
// verifies exactly MaxInflight succeed, ids are contiguous with no holes, and
// the same serialized result reproduces deterministically.
func TestConcurrentTriggersRespectLimit(t *testing.T) {
	const limit = 3
	run := func() (map[uint64]bool, string) {
		c := newForTest(t, Config{
			Tasks: 1, MaxInflight: limit, MinInterval: 0, Timeout: time.Hour,
			ToleratedTimeouts: 5, Retention: 1,
		})
		var wg sync.WaitGroup
		var mu sync.Mutex
		ids := make(map[uint64]bool)
		full := 0
		start := make(chan struct{})
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				id, err := c.Trigger()
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ids[id] = true
				case errors.Is(err, ErrConcurrencyFull):
					full++
				default:
					t.Errorf("unexpected trigger error: %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		snap := c.Query()
		if len(ids) != limit {
			t.Fatalf("accepted %d triggers, want %d", len(ids), limit)
		}
		if full != 20-limit {
			t.Fatalf("full rejections = %d, want %d", full, 20-limit)
		}
		for id := uint64(1); id <= limit; id++ {
			if !ids[id] {
				t.Fatalf("id %d missing; accepted ids must be contiguous 1..%d", id, limit)
			}
		}
		if snap.NextID != limit+1 {
			t.Fatalf("next id = %d, want %d (no holes, rejects consume none)", snap.NextID, limit+1)
		}
		if len(snap.InflightIDs) != limit {
			t.Fatalf("inflight = %d, want %d", len(snap.InflightIDs), limit)
		}
		return ids, fingerprint(snap)
	}
	_, fp1 := run()
	_, fp2 := run()
	if fp1 != fp2 {
		t.Fatalf("concurrent run fingerprints differ:\n%s\n%s", fp1, fp2)
	}
}

// TestReplayDeterminism replays an identical interleaved operation script
// twice and asserts the resulting state is byte-for-byte identical.
func TestReplayDeterminism(t *testing.T) {
	script := func(c *Coordinator) {
		mustTrigger(t, c)
		c.Ack(1, 0)
		_, _ = c.Trigger()
		c.Advance(5)
		mustTrigger(t, c)
		c.Ack(3, 0)
		c.Advance(10)
		_, _ = c.Trigger()
		_ = c.Ack(2, 0)
		cp, _ := c.Recover()
		c.Advance(20)
		id, err := c.Trigger()
		if err == nil {
			_ = c.Ack(id, 0)
		}
		_ = cp
		_ = c.Ack(999, 0)
	}
	cfg := Config{
		Tasks: 1, MaxInflight: 2, MinInterval: 3, Timeout: 7,
		ToleratedTimeouts: 1, Retention: 2,
	}
	run := func() string {
		c := newForTest(t, cfg)
		script(c)
		return fingerprint(c.Query())
	}
	if fp1, fp2 := run(), run(); fp1 != fp2 {
		t.Fatalf("replay mismatch:\n%s\n%s", fp1, fp2)
	}
}

// TestInvalidConfig rejects unusable configurations up front.
func TestInvalidConfig(t *testing.T) {
	bad := []Config{
		{Tasks: 0, MaxInflight: 1, Retention: 1, Timeout: 1},
		{Tasks: 1, MaxInflight: 0, Retention: 1, Timeout: 1},
		{Tasks: 1, MaxInflight: 1, Retention: 0, Timeout: 1},
		{Tasks: 1, MaxInflight: 1, Retention: 1, Timeout: -1},
		{Tasks: 1, MaxInflight: 1, Retention: 1, Timeout: 1, MinInterval: -1},
	}
	for index, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: err = %v, want ErrInvalidConfig", index, err)
		}
	}
}

// fingerprint renders a complete ordered state summary for equality checks.
func fingerprint(s Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "clock=%s failed=%t next=%d consec=%d\n",
		s.Clock, s.Failed, s.NextID, s.ConsecutiveTimeouts)
	fmt.Fprintf(&b, "inflight=%v retained=%v\n", s.InflightIDs, s.RetainedIDs)
	ids := make([]uint64, 0, len(s.Checkpoints))
	for id := range s.Checkpoints {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := s.Checkpoints[id]
		fmt.Fprintf(&b, "cp %d: status=%d at=%s end=%s abort=%d acks=%v\n",
			id, v.Status, v.TriggeredAt, v.EndedAt, v.AbortReason, v.AckedTasks)
	}
	return b.String()
}
