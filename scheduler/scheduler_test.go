package scheduler

import (
	"bytes"
	"errors"
	"io"
	"log"
	"testing"
)

func newTestScheduler(t *testing.T, n int) (*Scheduler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := log.New(io.MultiWriter(&buf), "", 0)
	s, err := New(n, logger)
	if err != nil {
		t.Fatalf("New(%d) unexpected error: %v", n, err)
	}
	return s, &buf
}

func mustSubmit(t *testing.T, s *Scheduler, id string, nodes int, dur Tick) []StartEvent {
	t.Helper()
	starts, err := s.Submit(Job{ID: id, Nodes: nodes, Duration: dur})
	if err != nil {
		t.Fatalf("Submit(%s) unexpected error: %v", id, err)
	}
	return starts
}

func assertReason(t *testing.T, events []StartEvent, index int, id string, reason StartReason) {
	t.Helper()
	if index >= len(events) {
		t.Fatalf("want start[%d] for %s (%s), got %d events: %v", index, id, reason, len(events), events)
	}
	if events[index].JobID != id || events[index].Reason != reason {
		t.Fatalf("start[%d] = %+v, want id=%s reason=%s", index, events[index], id, reason)
	}
}

// TestRejections verifies every distinguishable rejection reason and that a
// rejected operation changes neither the queue nor the running set.
func TestRejections(t *testing.T) {
	if _, err := New(0, nil); !errors.Is(err, ErrInvalidNodes) {
		t.Fatalf("New(0) error = %v, want ErrInvalidNodes", err)
	}
	if _, err := New(-3, nil); !errors.Is(err, ErrInvalidNodes) {
		t.Fatalf("New(-3) error = %v, want ErrInvalidNodes", err)
	}

	s, _ := newTestScheduler(t, 4)

	if _, err := s.Submit(Job{ID: "zero", Nodes: 0, Duration: 1}); !errors.Is(err, ErrInvalidJobNodes) {
		t.Fatalf("nodes=0 error = %v, want ErrInvalidJobNodes", err)
	}
	if _, err := s.Submit(Job{ID: "big", Nodes: 5, Duration: 1}); !errors.Is(err, ErrInvalidJobNodes) {
		t.Fatalf("nodes=5>N error = %v, want ErrInvalidJobNodes", err)
	}
	if _, err := s.Submit(Job{ID: "baddur", Nodes: 1, Duration: 0}); !errors.Is(err, ErrInvalidDuration) {
		t.Fatalf("duration=0 error = %v, want ErrInvalidDuration", err)
	}

	mustSubmit(t, s, "dup", 2, 3)
	if _, err := s.Submit(Job{ID: "dup", Nodes: 1, Duration: 1}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate id error = %v, want ErrDuplicateID", err)
	}

	if _, _, err := s.Finish("ghost"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Finish(ghost) error = %v, want ErrNotRunning", err)
	}

	if _, _, err := s.Advance(2); err != nil {
		t.Fatalf("Advance(2) error: %v", err)
	}
	if _, _, err := s.Advance(1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Advance(1) error = %v, want ErrClockRollback", err)
	}

	snap := s.Query()
	if snap.Time != 2 || len(snap.RunningIDs) != 1 || snap.RunningIDs[0] != "dup" {
		t.Fatalf("state after rejections = %+v, want only dup running at t=2", snap)
	}
}

// TestBackfillEndsExactlyAtShadow covers the required case: a job whose
// estimated end is exactly the head's shadow time may backfill.
func TestBackfillEndsExactlyAtShadow(t *testing.T) {
	s, _ := newTestScheduler(t, 8)

	// A occupies 4 nodes until t=5; 4 nodes are free now.
	mustSubmit(t, s, "A", 4, 5)
	// Head H needs 6 nodes and is blocked: earliest feasible start = 5.
	mustSubmit(t, s, "H", 6, 2)
	// F needs the 4 free nodes and runs 5 ticks: end at exactly shadow 5.
	starts := mustSubmit(t, s, "F", 4, 5)

	assertReason(t, starts, 0, "F", StartBackfillTime)
	if snap := s.Query(); len(snap.QueuedIDs) != 1 || snap.QueuedIDs[0] != "H" {
		t.Fatalf("queue after backfill = %v, want [H]", snap.QueuedIDs)
	}

	// At t=5 everything force-finishes and H starts at its shadow time.
	finishes, hStarts, err := s.Advance(5)
	if err != nil {
		t.Fatalf("Advance(5) error: %v", err)
	}
	if len(finishes) != 2 {
		t.Fatalf("finishes = %v, want A,F", finishes)
	}
	if len(hStarts) != 1 || hStarts[0].JobID != "H" {
		t.Fatalf("starts at shadow = %v, want H", hStarts)
	}
	if hStarts[0].Time > 5 {
		t.Fatalf("H started at %d, shadow bound was 5", hStarts[0].Time)
	}
}

// TestBackfillSpareSharing covers multiple backfill jobs splitting the spare
// node count that remains after the head is placed at the shadow time.
func TestBackfillSpareSharing(t *testing.T) {
	s, _ := newTestScheduler(t, 10)

	// A holds 6 nodes until t=3; 4 nodes are free now. H needs 8 nodes, so
	// releasing A (end 3) gives room: shadow = 3 and spare = 10 - 8 = 2.
	mustSubmit(t, s, "A", 6, 3)
	mustSubmit(t, s, "H", 8, 1)

	// Two long jobs split the 2-node spare budget in submission order.
	startsX := mustSubmit(t, s, "X", 1, 100) // spare 2 -> 1
	startsZ := mustSubmit(t, s, "Z", 1, 100) // spare 1 -> 0
	assertReason(t, startsX, 0, "X", StartBackfillSpare)
	assertReason(t, startsZ, 0, "Z", StartBackfillSpare)

	// Y fits right now (2 nodes are still free), but the spare budget is
	// exhausted and it runs past the shadow time: it must stay queued.
	startsY := mustSubmit(t, s, "Y", 2, 100)
	if len(startsY) != 0 {
		t.Fatalf("Y backfilled unexpectedly: %v", startsY)
	}
	if snap := s.Query(); len(snap.QueuedIDs) != 2 ||
		snap.QueuedIDs[0] != "H" || snap.QueuedIDs[1] != "Y" {
		t.Fatalf("queue = %v, want [H Y]", snap.QueuedIDs)
	}

	// Early finish of A recomputes the reservation: X/Z occupy only 2 nodes, so
	// H fits at once and starts strictly before its original shadow time.
	finishes, hStarts, err := s.Finish("A")
	if err != nil {
		t.Fatalf("Finish(A) error: %v", err)
	}
	if len(finishes) != 1 || finishes[0].JobID != "A" || finishes[0].Forced {
		t.Fatalf("finishes = %v, want early A", finishes)
	}
	if len(hStarts) != 1 || hStarts[0].JobID != "H" || hStarts[0].Time != 0 {
		t.Fatalf("starts after early free = %v, want H at t=0", hStarts)
	}
	// X1+Y3+H4 fills the cluster, so Z stays queued until X/Y finish.
	if snap := s.Query(); len(snap.QueuedIDs) != 1 || snap.QueuedIDs[0] != "Y" {
		t.Fatalf("queue = %v, want [Y]", snap.QueuedIDs)
	}
}

// TestEarlyFinishRecompute verifies an early finish triggers reservation
// recomputation and lets the head start before its original shadow time.
func TestEarlyFinishRecompute(t *testing.T) {
	s, _ := newTestScheduler(t, 4)
	mustSubmit(t, s, "A", 4, 10)
	mustSubmit(t, s, "H", 4, 1) // shadow = 10, no room to backfill

	finishes, starts, err := s.Finish("A")
	if err != nil {
		t.Fatalf("Finish error: %v", err)
	}
	if len(finishes) != 1 || finishes[0].Forced {
		t.Fatalf("finishes = %v, want one early finish", finishes)
	}
	if len(starts) != 1 || starts[0].JobID != "H" || starts[0].Time != 0 {
		t.Fatalf("starts = %v, want H at t=0", starts)
	}
}

// TestForceFinish verifies jobs are force-ended exactly when the clock reaches
// their estimated end time and never run past it.
func TestForceFinish(t *testing.T) {
	s, _ := newTestScheduler(t, 2)
	mustSubmit(t, s, "A", 2, 4)

	if _, starts, err := s.Advance(3); err != nil || len(starts) != 0 {
		t.Fatalf("at t=3: err=%v starts=%v, want A still running", err, starts)
	}
	if snap := s.Query(); len(snap.RunningIDs) != 1 || snap.RunningIDs[0] != "A" {
		t.Fatalf("A should still run at t=3: %+v", snap)
	}

	finishes, _, err := s.Advance(4)
	if err != nil {
		t.Fatalf("Advance(4) error: %v", err)
	}
	if len(finishes) != 1 || !finishes[0].Forced || finishes[0].Time != 4 {
		t.Fatalf("finishes = %v, want forced A at t=4", finishes)
	}
	if snap := s.Query(); len(snap.RunningIDs) != 0 {
		t.Fatalf("A must be gone at t=4: %+v", snap)
	}
}

// TestNoOvercommit checks the node sum invariant across a mixed scenario.
func TestNoOvercommit(t *testing.T) {
	s, _ := newTestScheduler(t, 5)
	mustSubmit(t, s, "A", 3, 2)
	mustSubmit(t, s, "B", 2, 8)
	mustSubmit(t, s, "H", 5, 1)
	mustSubmit(t, s, "F", 2, 2) // ends at t=2 == shadow from A; fits now? used=5, no

	if snap := s.Query(); snap.UsedNodes != 5 {
		t.Fatalf("used = %d, want 5", snap.UsedNodes)
	}
	if _, starts, err := s.Advance(2); err != nil {
		t.Fatalf("Advance(2) error: %v", err)
	} else {
		// A force-finishes; H starts (B occupies 2, H needs 5 -> does not
		// fit: 2+5>5). H stays queued; F fits now (2 used) and backfills
		// since it ends at 4 before H's shadow 8.
		if len(starts) != 1 || starts[0].JobID != "F" {
			t.Fatalf("starts = %v, want F backfill", starts)
		}
		if snap := s.Query(); snap.UsedNodes != 4 { // B 2 + F 2
			t.Fatalf("used = %d, want 4", snap.UsedNodes)
		}
	}
}

// TestLogging verifies the log carries inputs, outputs and decision reasons.
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	s, err := New(4, logger)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if _, err := s.Submit(Job{ID: "A", Nodes: 5, Duration: 1}); !errors.Is(err, ErrInvalidJobNodes) {
		t.Fatalf("Submit(A) error = %v, want ErrInvalidJobNodes", err)
	}
	logText := buf.String()
	for _, want := range []string{"input Submit", "output Submit", "rejected reason", "job nodes must be in"} {
		if !bytes.Contains([]byte(logText), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}

	buf.Reset()
	if _, err := s.Submit(Job{ID: "B", Nodes: 2, Duration: 2}); err != nil {
		t.Fatalf("Submit(B) error: %v", err)
	}
	logText = buf.String()
	for _, want := range []string{"input Submit", "output Submit", "decision start", "immediate"} {
		if !bytes.Contains([]byte(logText), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}
