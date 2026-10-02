package scheduler

import (
	"errors"
	"testing"
)

// Preemption requires e strictly greater than the shield: e == sh does not
// preempt.
func TestPreemptionStrictlyGreater(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 2, 4, 10) // runner, th=4
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 2, 4, 4, 1) // e = 4 == sh = 4
	expectStep(t, s, 1, 0)
	if d := s.LastDecision(); d.Reason != "shielded" || d.Preempted {
		t.Fatalf("e == sh must not preempt: %+v", d)
	}
	if got := s.tasks[1].pc; got != 0 {
		t.Fatalf("runner pc = %d, want 0", got)
	}
	mustAdd(t, s, 3, 5, 5, 1) // e = 5 > 4 preempts
	expectStep(t, s, 3, 3)
	if got := s.tasks[1].pc; got != 1 {
		t.Fatalf("runner pc = %d, want 1", got)
	}
}

// A threshold above the runner's own priority blocks middle-priority
// arrivals.
func TestThresholdBlocksMiddlePriority(t *testing.T) {
	s := mustNew(t, 5, 0, 2, 10)
	mustAdd(t, s, 1, 1, 3, 10) // p=1 but th=3
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 2, 2, 2, 1) // p=2 > p(A)=1 but e=2 <= sh=3
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 3, 3, 3, 1) // e=3 <= sh=3, still blocked
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 4, 4, 4, 1) // e=4 > 3 preempts
	expectStep(t, s, 4, 4)
}

// The wait bonus is floor(wt/W) capped at Bmax.
func TestBonusFloorAndCap(t *testing.T) {
	s := mustNew(t, 3, 2, 1, 10)
	mustAdd(t, s, 1, 10, 10, 100)                    // permanent runner
	mustAdd(t, s, 2, 1, 1, 1)                        // waiter
	wantBonus := []int{0, 0, 1, 1, 1, 2, 2, 2, 2, 2} // floor(wt/3), wt=1..10, cap 2
	for k := 1; k <= 10; k++ {
		run, _ := s.Step()
		if run != 1 {
			t.Fatalf("tick %d: run = %d, want 1", k, run)
		}
		wt := s.ready[2].wt
		if wt != int64(k) {
			t.Fatalf("tick %d: wt = %d, want %d", k, wt, k)
		}
		if got := s.ready[2].bonus; got != wantBonus[k-1] {
			t.Fatalf("tick %d: bonus = %d, want %d (wt=%d)", k, got, wantBonus[k-1], wt)
		}
	}
	// Decision at tick 12 start: wt=11, e = 1 + min(2, 11/3=3) = 3 (capped).
	run, _ := s.Step()
	if d := s.LastDecision(); d.SigmaID != 2 || d.SigmaE != 3 {
		t.Fatalf("decision = (sigma=%d,e=%d), want (2,3)", d.SigmaID, d.SigmaE)
	}
	_ = run
}

// wt resets to zero both when dispatched and when preempted.
func TestWaitCounterReset(t *testing.T) {
	s := mustNew(t, 1, 2, 5, 10)
	mustAdd(t, s, 1, 1, 5, 100)
	expectStep(t, s, 1, 0) // A dispatched: wt=0
	if got := s.running.wt; got != 0 {
		t.Fatalf("running wt = %d, want 0", got)
	}
	mustAdd(t, s, 2, 5, 5, 1)
	expectStep(t, s, 1, 0) // e(B)=5 <= sh(A)=5: B waits, wt=1
	if got := s.ready[2].wt; got != 1 {
		t.Fatalf("B.wt = %d, want 1", got)
	}
	expectStep(t, s, 2, 2) // e(B)=5+1=6 > 5: A preempted (wt reset), B dispatched
	if got := s.ready[1].wt; got != 1 {
		t.Fatalf("A.wt after preempt tick = %d, want 1 (0 then +1)", got)
	}
	if got := s.tasks[1].pc; got != 1 {
		t.Fatalf("A.pc = %d, want 1", got)
	}
	expectStep(t, s, 1, 0) // A re-dispatched: wt reset to 0
	if got := s.running.wt; got != 0 {
		t.Fatalf("running wt = %d, want 0", got)
	}
}

// Add between two Steps: the fresh task takes part in the next decision
// with wt=0 and accumulates its first wt in that tick's execution phase.
func TestAddBetweenStepsFreshTask(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	mustAdd(t, s, 1, 5, 5, 10)
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 2, 1, 1, 1) // arrives at now=1
	// tick 2 decision sees B with wt=0 -> e = p = 1.
	run, _ := s.Step()
	if d := s.LastDecision(); d.SigmaID != 2 || d.SigmaE != 1 {
		t.Fatalf("decision = (sigma=%d,e=%d), want (2,1)", d.SigmaID, d.SigmaE)
	}
	if run != 1 {
		t.Fatalf("run = %d, want 1", run)
	}
	if got := s.ready[2].wt; got != 1 {
		t.Fatalf("B.wt after its first tick = %d, want 1", got)
	}
}

// After M preemptions the runner is protected (shield = infinity) and can
// never be preempted again.
func TestProtectionAfterMPreemptions(t *testing.T) {
	s := mustNew(t, 3, 0, 2, 10)
	mustAdd(t, s, 1, 1, 1, 100)
	expectStep(t, s, 1, 0)
	mustAdd(t, s, 2, 2, 2, 1)
	expectStep(t, s, 2, 2) // A preempted, pc=1
	expectStep(t, s, 1, 0) // A re-dispatched
	mustAdd(t, s, 3, 2, 2, 1)
	expectStep(t, s, 3, 3) // A preempted, pc=2 == M
	expectStep(t, s, 1, 0) // A re-dispatched
	mustAdd(t, s, 4, 255, 255, 1)
	expectStep(t, s, 1, 0) // protected: even e=255 cannot preempt
	d := s.LastDecision()
	if d.Shield != InfShield || d.Preempted || d.Reason != "shielded" {
		t.Fatalf("protected runner: %+v", d)
	}
	if got := s.tasks[1].pc; got != 2 {
		t.Fatalf("A.pc = %d, want 2", got)
	}
}

// Ties in effective priority are broken by the lower arrival sequence.
func TestTieBreakBySeq(t *testing.T) {
	s := mustNew(t, 3, 0, 2, 10)
	mustAdd(t, s, 1, 9, 9, 2) // runs ticks 1-2
	mustAdd(t, s, 2, 5, 5, 1) // seq 2
	mustAdd(t, s, 3, 5, 5, 1) // seq 3
	expectStep(t, s, 1, 0)
	expectStep(t, s, 1, 1)
	expectStep(t, s, 2, 2) // tie at e=5, seq 2 first
	expectStep(t, s, 3, 3)
}

// Dispatch with no running task does not consult any threshold.
func TestDispatchIgnoresThreshold(t *testing.T) {
	s := mustNew(t, 3, 0, 2, 10)
	mustAdd(t, s, 1, 0, 0, 1) // lowest possible priority and threshold
	expectStep(t, s, 1, 1)
	if d := s.LastDecision(); d.Reason != "idle-dispatch" {
		t.Fatalf("reason = %q, want idle-dispatch", d.Reason)
	}
}

// A task completes at time now+1 of its final tick and leaves the registry;
// its id can be reused afterwards.
func TestCompletionAtNowPlusOneAndIdReuse(t *testing.T) {
	s := mustNew(t, 3, 0, 2, 10)
	mustAdd(t, s, 7, 1, 1, 2)
	expectStep(t, s, 7, 0) // tick 1: completes at now+1 = 2? no, rem=1 left
	run, comp := s.Step()  // tick 2: completes at time 2
	logDecision(t, s, run, comp)
	if comp != 7 || s.Now() != 2 {
		t.Fatalf("comp=%d now=%d, want comp=7 now=2", comp, s.Now())
	}
	if _, ok := s.tasks[7]; ok {
		t.Fatal("completed task still registered")
	}
	mustAdd(t, s, 7, 1, 1, 1) // id reused
	expectStep(t, s, 7, 7)
}

// Step is never rejected: with no tasks it idles and still advances now.
func TestIdleStep(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 10)
	for k := 1; k <= 3; k++ {
		run, comp := s.Step()
		logDecision(t, s, run, comp)
		if run != 0 || comp != 0 {
			t.Fatalf("idle Step = (%d,%d), want (0,0)", run, comp)
		}
		if d := s.LastDecision(); d.Reason != "idle" {
			t.Fatalf("reason = %q, want idle", d.Reason)
		}
	}
	if got := s.Now(); got != 3 {
		t.Fatalf("now = %d, want 3", got)
	}
}

// Add reports errors in the order invalid-param, duplicate, full; rejected
// Adds change neither tasks nor the sequence counter.
func TestAddRejectionOrderAndAtomicity(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 2)
	mustAdd(t, s, 1, 1, 1, 1)
	mustAdd(t, s, 2, 2, 2, 2)

	cases := []struct {
		name         string
		id, p, th, w int
		want         error
	}{
		{"zero id", 0, 1, 1, 1, ErrInvalidParam},
		{"negative id", -3, 1, 1, 1, ErrInvalidParam},
		{"p too large", 9, 256, 256, 1, ErrInvalidParam},
		{"th below p", 9, 5, 4, 1, ErrInvalidParam},
		{"th too large", 9, 5, 256, 1, ErrInvalidParam},
		{"zero work", 9, 1, 1, 0, ErrInvalidParam},
		{"work too large", 9, 1, 1, 1_000_001, ErrInvalidParam},
		{"invalid and duplicate", 1, 9, 1, 1, ErrInvalidParam}, // param first
		{"duplicate and full", 1, 1, 1, 1, ErrDuplicate},       // duplicate before full
		{"full", 3, 1, 1, 1, ErrFull},
	}
	for _, c := range cases {
		if err := s.Add(c.id, c.p, c.th, c.w); !errors.Is(err, c.want) {
			t.Fatalf("%s: Add error = %v, want %v", c.name, err, c.want)
		}
	}
	if got := s.seqNext; got != 3 {
		t.Fatalf("seqNext = %d, want 3 (rejected Adds must not consume seq)", got)
	}
	if len(s.tasks) != 2 || len(s.ready) != 2 {
		t.Fatalf("registry changed by rejected Adds: tasks=%d ready=%d", len(s.tasks), len(s.ready))
	}
	if got := s.Now(); got != 0 {
		t.Fatalf("now = %d, want 0", got)
	}
	// A completed id frees registry space and can be reused.
	expectStep(t, s, 2, 0) // highest e first: task 2 (p=2)
	expectStep(t, s, 2, 2)
	mustAdd(t, s, 2, 3, 3, 1)
}

// Constructor arguments out of range reject the whole configuration.
func TestInvalidConfig(t *testing.T) {
	bad := [][4]int{
		{0, 0, 1, 1}, {1_000_001, 0, 1, 1},
		{1, -1, 1, 1}, {1, 1001, 1, 1},
		{1, 0, 0, 1}, {1, 0, 1001, 1},
		{1, 0, 1, 0}, {1, 0, 1, 1_000_001},
	}
	for _, c := range bad {
		if _, err := New(c[0], c[1], c[2], c[3]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New%v = %v, want ErrInvalidConfig", c, err)
		}
	}
	if _, err := New(1, 0, 1, 1); err != nil {
		t.Fatalf("minimal valid config: %v", err)
	}
	if _, err := New(1_000_000, 1000, 1000, 1_000_000); err != nil {
		t.Fatalf("maximal valid config: %v", err)
	}
}

// Replaying the same operation sequence yields identical runs and
// completion times.
func TestDeterminism(t *testing.T) {
	scenario := func() [][2]int {
		s := mustNew(t, 3, 2, 2, 10)
		var trace [][2]int
		mustAdd(t, s, 1, 1, 3, 5)
		mustAdd(t, s, 2, 2, 2, 2)
		for k := 0; k < 12; k++ {
			if k == 2 {
				mustAdd(t, s, 3, 4, 4, 1)
			}
			if k == 5 {
				mustAdd(t, s, 4, 1, 1, 3)
			}
			run, comp := s.Step()
			trace = append(trace, [2]int{run, comp})
		}
		return trace
	}
	a, b := scenario(), scenario()
	if len(a) != len(b) {
		t.Fatal("trace length mismatch")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tick %d: %v != %v", i+1, a[i], b[i])
		}
	}
}
