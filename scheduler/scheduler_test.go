package scheduler

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, w, bmax, m, tmax int) *Scheduler {
	t.Helper()
	s, err := NewScheduler(w, bmax, m, tmax)
	if err != nil {
		t.Fatalf("NewScheduler(%d,%d,%d,%d): %v", w, bmax, m, tmax, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Scheduler, id, p, th, w int) {
	t.Helper()
	if err := s.Add(id, p, th, w); err != nil {
		t.Fatalf("Add(%d,%d,%d,%d): %v", id, p, th, w, err)
	}
}

func stepEq(t *testing.T, s *Scheduler, wantRun, wantDone int) {
	t.Helper()
	run, done := s.Step()
	if run != wantRun || done != wantDone {
		t.Fatalf("tick %d: Step()=(%d,%d), want (%d,%d)", s.Now(), run, done, wantRun, wantDone)
	}
}

// The worked example from the spec: W=3, Bmax=2, M=2.
func TestSpecExamplePreemption(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 1, 3, 5) // A
	stepEq(t, s, 1, 0)        // tick 1: dispatch A
	mustAdd(t, s, 2, 2, 2, 2) // B
	stepEq(t, s, 1, 0)        // tick 2: e(B)=2 <= sh=3, A continues
	mustAdd(t, s, 3, 4, 4, 1) // C
	stepEq(t, s, 3, 3)        // tick 3: e(C)=4 > 3, preempts A, C completes at 3
	if a := s.tasks[1]; a.pc != 1 {
		t.Fatalf("A.pc=%d, want 1", a.pc)
	}
	stepEq(t, s, 2, 0) // tick 4: e(A)=1, e(B)=2+floor(2/3)=2, dispatch B
	stepEq(t, s, 2, 2) // tick 5: e(A)=1 <= sh=2, B completes at 5
	stepEq(t, s, 1, 0) // tick 6: A resumes
	stepEq(t, s, 1, 0) // tick 7
	stepEq(t, s, 1, 1) // tick 8: A completes at 8
	stepEq(t, s, 0, 0) // idle
	if s.Now() != 9 {
		t.Fatalf("now=%d, want 9", s.Now())
	}
}

// The aging example from the spec: same parameters, fresh instance.
func TestSpecExampleAging(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 5, 5, 7) // Y
	mustAdd(t, s, 2, 1, 1, 1) // X
	stepEq(t, s, 1, 0)        // tick 1
	stepEq(t, s, 1, 0)        // tick 2
	stepEq(t, s, 1, 0)        // tick 3
	mustAdd(t, s, 3, 2, 2, 1) // Z arrives at now=3
	stepEq(t, s, 1, 0)        // tick 4
	stepEq(t, s, 1, 0)        // tick 5
	stepEq(t, s, 1, 0)        // tick 6
	stepEq(t, s, 1, 1)        // tick 7: Y completes at 7
	// Y completed at 7. Tick 8: X has wt=7, e=1+min(2,2)=3;
	// Z has wt=4, e=2+min(2,1)=3; tie broken by seq -> X first.
	stepEq(t, s, 2, 2) // tick 8: X dispatched and completes at 8
	stepEq(t, s, 3, 3) // tick 9: Z
	stepEq(t, s, 0, 0)
}

// e == sh must not preempt: the comparison is strictly greater.
func TestStrictlyGreaterPreemption(t *testing.T) {
	s := mustNew(t, 3, 0, 2, 10) // Bmax=0: no aging, B stays at e=3 == sh
	mustAdd(t, s, 1, 1, 3, 5)    // A: p=1, th=3
	stepEq(t, s, 1, 0)
	mustAdd(t, s, 2, 3, 3, 1) // B: e=3 == sh=3
	stepEq(t, s, 1, 0)        // no preemption
	if a := s.tasks[1]; a.pc != 0 {
		t.Fatalf("A.pc=%d, want 0", a.pc)
	}
	stepEq(t, s, 1, 0)
	stepEq(t, s, 1, 0)
	stepEq(t, s, 1, 1) // A completes at 5
	stepEq(t, s, 2, 2) // B runs and completes at 6
}

// A threshold above the runner's own priority blocks mid-priority arrivals.
func TestThresholdBlocksMidPriority(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 1, 5, 4) // A: p=1, th=5
	stepEq(t, s, 1, 0)
	mustAdd(t, s, 2, 3, 3, 1) // B: e=3 <= sh=5, blocked
	stepEq(t, s, 1, 0)
	mustAdd(t, s, 3, 6, 6, 1) // C: e=6 > sh=5, preempts
	stepEq(t, s, 3, 3)
	if a := s.tasks[1]; a.pc != 1 {
		t.Fatalf("A.pc=%d, want 1", a.pc)
	}
	stepEq(t, s, 2, 2) // tick 4: no runner, sigma is B (e=3 > A's 1), done at 4
	stepEq(t, s, 1, 0) // tick 5: A resumes
	stepEq(t, s, 1, 1) // tick 6: A completes at 6 (ran ticks 1,2,5,6)
}

// Bonus is floor(wt/W) capped at Bmax.
func TestAgingFloorAndCap(t *testing.T) {
	s := mustNew(t, 2, 1, 5, 10) // W=2, Bmax=1
	mustAdd(t, s, 1, 5, 5, 20)   // A runs forever-ish, sh=5
	mustAdd(t, s, 2, 5, 5, 1)    // B: same p, loses the seq tie
	wtOf := func(id int) int { return s.now - s.tasks[id].epoch }
	eOf := func(id int) int { return s.tasks[id].e }
	stepEq(t, s, 1, 0)               // tick 1; B wt=0 at decision
	if wtOf(2) != 1 || eOf(2) != 5 { // floor(1/2)=0
		t.Fatalf("after tick1: B wt=%d e=%d, want wt=1 e=5", wtOf(2), eOf(2))
	}
	stepEq(t, s, 1, 0)               // tick 2 decision: B wt=1, e=5, not > sh=5
	if wtOf(2) != 2 || eOf(2) != 6 { // floor(2/2)=1
		t.Fatalf("after tick2: B wt=%d e=%d, want wt=2 e=6", wtOf(2), eOf(2))
	}
	stepEq(t, s, 2, 2) // tick 3 decision: B wt=2, e=6 > sh=5 -> preempt, B done at 3
	if a := s.tasks[1]; a.pc != 1 {
		t.Fatalf("A.pc=%d, want 1", a.pc)
	}
	// Cap: bonus never exceeds Bmax=1 even after long waits.
	s2 := mustNew(t, 1, 1, 5, 10)
	mustAdd(t, s2, 1, 9, 9, 50)
	mustAdd(t, s2, 2, 1, 1, 1)
	for i := 0; i < 10; i++ {
		s2.Step()
	}
	if e := s2.tasks[2].e; e != 2 { // p=1 + cap 1
		t.Fatalf("capped e=%d, want 2", e)
	}
}

// wt resets to 0 both on dispatch and on preemption.
func TestWaitResetOnDispatchAndPreempt(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 1, 3, 5) // A
	stepEq(t, s, 1, 0)
	mustAdd(t, s, 2, 2, 2, 2) // B
	stepEq(t, s, 1, 0)
	mustAdd(t, s, 3, 4, 4, 1) // C
	stepEq(t, s, 3, 3)        // C preempts A at now=2
	if wt := s.now - s.tasks[1].epoch; wt != 1 {
		t.Fatalf("A wt after preemption tick = %d, want 1 (reset then aged once)", wt)
	}
	stepEq(t, s, 2, 0) // B dispatched at now=3
	if wt := s.now - s.tasks[2].epoch; wt != 1 {
		t.Fatalf("B wt after dispatch tick = %d, want 1 (reset then aged once)", wt)
	}
}

// A task added between two Steps is decided with wt=0 at the next tick but
// already accumulates wt in that tick's execution phase.
func TestArrivalAgingWithinTick(t *testing.T) {
	s := mustNew(t, 1, 5, 5, 10) // W=1
	mustAdd(t, s, 1, 5, 5, 10)   // A: sh=5
	stepEq(t, s, 1, 0)           // tick 1
	mustAdd(t, s, 2, 5, 5, 1)    // B arrives at now=1
	stepEq(t, s, 1, 0)           // tick 2 decision uses B wt=0: e=5 not > 5
	if wt := s.now - s.tasks[2].epoch; wt != 1 {
		t.Fatalf("B wt after its first execution phase = %d, want 1", wt)
	}
	stepEq(t, s, 2, 2) // tick 3: B wt=1, e=6 > 5 -> preempts, done at 3
}

// After M preemptions the shield becomes infinite.
func TestProtectionAfterMPreemptions(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10) // M=2
	mustAdd(t, s, 1, 1, 1, 10)   // A: p=1, th=1
	stepEq(t, s, 1, 0)           // tick 1
	mustAdd(t, s, 2, 2, 2, 1)    // B
	stepEq(t, s, 2, 2)           // tick 2: B preempts A (pc=1), done at 2
	stepEq(t, s, 1, 0)           // tick 3: A resumes
	mustAdd(t, s, 3, 2, 2, 1)    // C
	stepEq(t, s, 3, 3)           // tick 4: C preempts A (pc=2), done at 4
	stepEq(t, s, 1, 0)           // tick 5: A resumes, now protected
	mustAdd(t, s, 4, 2, 2, 1)    // D: e=2 but A's sh is infinite
	for i := 0; i < 6; i++ {
		stepEq(t, s, 1, 0) // ticks 6..11: A cannot be preempted
	}
	stepEq(t, s, 1, 1) // tick 12: A completes (ran ticks 1,3,5,6..12)
	stepEq(t, s, 4, 4) // tick 13: D finally runs
}

// Ties on effective priority go to the smaller seq.
func TestTieBreakBySeq(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 7, 1, 1, 1) // seq 1
	mustAdd(t, s, 3, 1, 1, 1) // seq 2
	stepEq(t, s, 7, 7)
	stepEq(t, s, 3, 3)
}

// Dispatch with no runner ignores thresholds entirely.
func TestDispatchIgnoresThreshold(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 0, 255, 1) // lowest priority, highest threshold
	mustAdd(t, s, 2, 0, 255, 1)
	stepEq(t, s, 1, 1)
	stepEq(t, s, 2, 2)
}

// Completion is reported at now+1 and now advances on idle ticks.
func TestCompletionAndIdle(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	stepEq(t, s, 0, 0) // idle tick, now: 0 -> 1
	if s.Now() != 1 {
		t.Fatalf("now=%d, want 1", s.Now())
	}
	mustAdd(t, s, 1, 1, 1, 1) // arrives at now=1
	stepEq(t, s, 1, 1)        // completes at now+1 = 2
	if s.Now() != 2 {
		t.Fatalf("now=%d, want 2", s.Now())
	}
	if s.Count() != 0 {
		t.Fatalf("count=%d, want 0", s.Count())
	}
	// Completed ids can be reused.
	mustAdd(t, s, 1, 1, 1, 1)
	stepEq(t, s, 1, 1)
}

// Add reports the first applicable error and rejected Adds change nothing.
func TestAddRejectionOrderAndState(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 1) // Tmax=1
	mustAdd(t, s, 5, 1, 1, 2)
	seqBefore := s.seq
	nowBefore := s.Now()

	// Invalid params win over duplicate and full.
	if err := s.Add(5, -1, 0, 1); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
	if err := s.Add(0, 0, 0, 1); !errors.Is(err, ErrInvalidTask) {
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
	if err := s.Add(6, 2, 1, 1); !errors.Is(err, ErrInvalidTask) { // th < p
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
	if err := s.Add(6, 0, 0, 0); !errors.Is(err, ErrInvalidTask) { // w < 1
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
	// Duplicate wins over full.
	if err := s.Add(5, 1, 1, 1); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("err=%v, want ErrTaskExists", err)
	}
	// Full.
	if err := s.Add(6, 1, 1, 1); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v, want ErrFull", err)
	}
	if s.seq != seqBefore || s.Now() != nowBefore || s.Count() != 1 {
		t.Fatalf("rejected Add mutated state: seq=%d now=%d count=%d",
			s.seq, s.Now(), s.Count())
	}
	// The registered task is untouched and still runs.
	stepEq(t, s, 5, 0)
	stepEq(t, s, 5, 5)
	// After completion the id is reusable and seq kept increasing.
	mustAdd(t, s, 5, 1, 1, 1)
	if s.seq != seqBefore+1 {
		t.Fatalf("seq=%d, want %d", s.seq, seqBefore+1)
	}
}

func TestInvalidConfig(t *testing.T) {
	bad := [][4]int{
		{0, 2, 2, 10}, {1_000_001, 2, 2, 10},
		{3, -1, 2, 10}, {3, 1001, 2, 10},
		{3, 2, 0, 10}, {3, 2, 1001, 10},
		{3, 2, 2, 0}, {3, 2, 2, 1_000_001},
	}
	for _, c := range bad {
		if s, err := NewScheduler(c[0], c[1], c[2], c[3]); !errors.Is(err, ErrInvalidConfig) || s != nil {
			t.Fatalf("NewScheduler%v: (%v,%v), want ErrInvalidConfig", c, s, err)
		}
	}
}
