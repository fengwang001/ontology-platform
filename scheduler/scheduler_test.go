package scheduler

import (
	"fmt"
	"testing"
)

func mustNew(t *testing.T, W, Bmax, M, Tmax int) *Scheduler {
	t.Helper()
	s, err := New(W, Bmax, M, Tmax)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", W, Bmax, M, Tmax, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Scheduler, id, p, th, w int) {
	t.Helper()
	if err := s.Add(id, p, th, w); err != nil {
		t.Fatalf("Add(%d,%d,%d,%d): %v", id, p, th, w, err)
	}
}

func logDecision(t *testing.T, s *Scheduler, run, comp int) {
	t.Helper()
	d := s.LastDecision()
	sh := fmt.Sprintf("%d", d.Shield)
	if d.Shield == InfShield {
		sh = "inf"
	}
	t.Logf("tick=%d runner@start=%d sigma=%d e=%d shield=%s reason=%s preempted=%v -> run=%d completed=%d",
		d.Tick, d.RunnerID, d.SigmaID, d.SigmaE, sh, d.Reason, d.Preempted, run, comp)
}

func expectStep(t *testing.T, s *Scheduler, wantRun, wantComp int) {
	t.Helper()
	run, comp := s.Step()
	logDecision(t, s, run, comp)
	if run != wantRun || comp != wantComp {
		t.Fatalf("Step() = (%d,%d), want (%d,%d)", run, comp, wantRun, wantComp)
	}
}

// TestSpecExamplePreemption replays the worked example from the
// specification: W=3, Bmax=2, M=2 with A(1,3,5)@0, B(2,2,2)@1, C(4,4,1)@2.
func TestSpecExamplePreemption(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 1, 3, 5) // A
	expectStep(t, s, 1, 0)    // tick 1: dispatch A
	mustAdd(t, s, 2, 2, 2, 2) // B arrives at now=1
	expectStep(t, s, 1, 0)    // tick 2: e(B)=2 <= sh(A)=3, A continues
	mustAdd(t, s, 3, 4, 4, 1) // C arrives at now=2
	expectStep(t, s, 3, 3)    // tick 3: e(C)=4 > 3 preempts A, C completes at 3
	if got := s.tasks[1].pc; got != 1 {
		t.Fatalf("A.pc = %d, want 1", got)
	}
	if got := s.ready[1].wt; got != 1 {
		t.Fatalf("A.wt after preemption tick = %d, want 1 (reset then +1)", got)
	}
	expectStep(t, s, 2, 0) // tick 4: e(A)=1, e(B)=2+floor(2/3)=2, dispatch B
	expectStep(t, s, 2, 2) // tick 5: e(A)=1 <= sh(B)=2, B completes at 5
	expectStep(t, s, 1, 0) // tick 6
	expectStep(t, s, 1, 0) // tick 7
	expectStep(t, s, 1, 1) // tick 8: A completes at 8
	if got := s.Now(); got != 8 {
		t.Fatalf("now = %d, want 8", got)
	}
	if len(s.tasks) != 0 {
		t.Fatalf("registry not empty: %d tasks", len(s.tasks))
	}
}

// TestSpecExampleBonus replays the bonus example: Y(5,5,7) and X(1,1,1)
// arrive at 0, Z(2,2,1) arrives at 3; at tick 8 X and Z tie at e=3 and the
// lower sequence number wins (without the bonus Z would win).
func TestSpecExampleBonus(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 5, 5, 7) // Y
	mustAdd(t, s, 2, 1, 1, 1) // X
	expectStep(t, s, 1, 0)    // tick 1
	expectStep(t, s, 1, 0)    // tick 2
	expectStep(t, s, 1, 0)    // tick 3
	mustAdd(t, s, 3, 2, 2, 1) // Z arrives at now=3
	expectStep(t, s, 1, 0)    // tick 4
	expectStep(t, s, 1, 0)    // tick 5
	expectStep(t, s, 1, 0)    // tick 6
	expectStep(t, s, 1, 1)    // tick 7: Y completes at 7
	if got := s.ready[2].wt; got != 7 {
		t.Fatalf("X.wt at tick 8 start = %d, want 7", got)
	}
	if got := s.ready[3].wt; got != 4 {
		t.Fatalf("Z.wt at tick 8 start = %d, want 4", got)
	}
	expectStep(t, s, 2, 2) // tick 8: e(X)=1+min(2,2)=3, e(Z)=2+min(2,1)=3, seq wins
	if d := s.LastDecision(); d.SigmaID != 2 || d.SigmaE != 3 {
		t.Fatalf("tick 8 decision = (sigma=%d,e=%d), want (2,3)", d.SigmaID, d.SigmaE)
	}
	expectStep(t, s, 3, 3) // tick 9: Z dispatched and completes
}
