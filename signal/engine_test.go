package signal

import (
	"errors"
	"testing"
)

func mustBuild(t *testing.T, specs []PhaseSpec, plan Plan) *Intersection {
	t.Helper()
	x, err := NewIntersection(specs, plan, 0)
	if err != nil {
		t.Fatalf("NewIntersection: %v", err)
	}
	return x
}

func expectErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got error %v, want %v", what, got, want)
	}
}

func expectQuery(t *testing.T, x *Intersection, at int, want QueryResult) {
	t.Helper()
	if got := x.Query(at); got != want {
		t.Fatalf("Query(%d) = %+v, want %+v", at, got, want)
	}
}

func expectState(t *testing.T, x *Intersection, id string, at int, want RequestState) {
	t.Helper()
	got, ok := x.RequestState(id, at)
	if !ok {
		t.Fatalf("RequestState(%q, %d): not found, want %v", id, at, want)
	}
	if got != want {
		t.Fatalf("RequestState(%q, %d) = %v, want %v", id, at, got, want)
	}
}

// Set greens equal to MinGreen/MaxGreen are feasible; anything outside is
// not. Offset must be smaller than the cycle.
func TestPlanFeasibilityBoundaries(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 10, Clearance: 2}, {MinGreen: 8, MaxGreen: 20, Clearance: 3}}
	// cycle = 5+2+20+3 = 30
	if _, err := NewIntersection(specs, Plan{Greens: []int{5, 20}, Offset: 29}, 0); err != nil {
		t.Fatalf("min/max greens with offset 29 should be feasible: %v", err)
	}
	if _, err := NewIntersection(specs, Plan{Greens: []int{10, 8}, Offset: 0}, 0); err != nil {
		t.Fatalf("max/min greens should be feasible: %v", err)
	}
	cases := []Plan{
		{Greens: []int{4, 20}},             // below min
		{Greens: []int{11, 20}},            // above max
		{Greens: []int{5, 21}},             // above max
		{Greens: []int{5, 7}},              // below min
		{Greens: []int{5, 20}, Offset: 30}, // offset == cycle
		{Greens: []int{5, 20}, Offset: -1},
		{Greens: []int{5}}, // wrong arity
		{Greens: []int{5, 20}, MaxAdjustPerCycle: -1},
	}
	for i, p := range cases {
		if _, err := NewIntersection(specs, p, 0); !errors.Is(err, ErrInfeasiblePlan) {
			t.Fatalf("case %d: got %v, want ErrInfeasiblePlan", i, err)
		}
	}
	if _, err := NewIntersection([]PhaseSpec{{MinGreen: 5, MaxGreen: 10, Clearance: 2}}, Plan{Greens: []int{5}}, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("single phase: got %v, want ErrInvalidParam", err)
	}
	if _, err := NewIntersection(specs, Plan{Greens: []int{5, 20}}, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative start: got %v, want ErrInvalidParam", err)
	}
}

// A plan change accepted exactly at the cycle-end instant takes effect at
// the NEXT cycle end; queries before that still use the old plan.
func TestPlanChangeAtCycleBoundary(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 6, MaxGreen: 10, Clearance: 2}, {MinGreen: 6, MaxGreen: 10, Clearance: 2}}
	x := mustBuild(t, specs, Plan{Greens: []int{6, 6}, Offset: 0, MaxAdjustPerCycle: 2}) // C=16
	planB := Plan{Greens: []int{8, 8}, Offset: 0, MaxAdjustPerCycle: 2}                  // C=20
	if err := x.ChangePlan(planB, 16); err != nil {
		t.Fatalf("ChangePlan at cycle end: %v", err)
	}
	// Old plan still governs the cycle that started at 16.
	expectQuery(t, x, 17, QueryResult{Phase: 0, Elapsed: 1, Remaining: 7})
	expectQuery(t, x, 30, QueryResult{Phase: 1, Elapsed: 6, Remaining: 2})
	// New plan effective from the wrap at 32.
	expectQuery(t, x, 33, QueryResult{Phase: 0, Elapsed: 1, Remaining: 9})
}

// A pending plan replaced before taking effect is silently swapped and the
// replacement is queryable.
func TestPlanChangeReplacementQueryable(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 6, MaxGreen: 10, Clearance: 2}, {MinGreen: 6, MaxGreen: 10, Clearance: 2}}
	x := mustBuild(t, specs, Plan{Greens: []int{6, 6}, Offset: 0, MaxAdjustPerCycle: 2})
	planB := Plan{Greens: []int{8, 8}, Offset: 0, MaxAdjustPerCycle: 2}
	planC := Plan{Greens: []int{7, 7}, Offset: 0, MaxAdjustPerCycle: 1}
	if err := x.ChangePlan(planB, 1); err != nil {
		t.Fatal(err)
	}
	if err := x.ChangePlan(planC, 2); err != nil {
		t.Fatal(err)
	}
	pending, replaced := x.PendingPlan()
	if pending == nil || replaced == nil {
		t.Fatalf("PendingPlan = (%v, %v), want both set", pending, replaced)
	}
	if pending.Greens[0] != 7 || replaced.Greens[0] != 8 {
		t.Fatalf("pending=%+v replaced=%+v, want C pending and B replaced", pending, replaced)
	}
	// After the wrap at 16 the pending plan is consumed.
	if err := x.ConfirmPassage("nobody", 20); err != nil {
		t.Fatal(err)
	}
	pending, replaced = x.PendingPlan()
	if pending != nil || replaced == nil || replaced.Greens[0] != 8 {
		t.Fatalf("after wrap: pending=%v replaced=%v", pending, replaced)
	}
	expectQuery(t, x, 17, QueryResult{Phase: 0, Elapsed: 1, Remaining: 8}) // planC greens 7+2
}

// Emergency request whose target is the current phase extends the green to
// MaxGreen and holds until confirmation.
func TestEmergencyTargetIsCurrentPhase(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 12, Clearance: 1}, {MinGreen: 5, MaxGreen: 15, Clearance: 1}, {MinGreen: 5, MaxGreen: 20, Clearance: 1}}
	x := mustBuild(t, specs, Plan{Greens: []int{8, 9, 10}, Offset: 0, MaxAdjustPerCycle: 3}) // C=30
	// Phase 1 runs [9,18); request at 10 extends it to 9+15=24.
	if err := x.RequestEmergency("E1", 1, 10); err != nil {
		t.Fatal(err)
	}
	expectQuery(t, x, 10, QueryResult{Phase: 1, Elapsed: 1, Remaining: 15})
	expectState(t, x, "E1", 10, StateServing)
	if err := x.ConfirmPassage("E1", 12); err != nil {
		t.Fatal(err)
	}
	expectQuery(t, x, 13, QueryResult{Phase: 2, Elapsed: 0, Remaining: 11})
	expectState(t, x, "E1", 13, StateCompleted)
}

// A phase skipped in one cycle may not be skipped in the next cycle.
func TestConsecutiveSkipRejected(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}}
	x := mustBuild(t, specs, Plan{Greens: []int{5, 5, 5}, Offset: 0, MaxAdjustPerCycle: 10}) // C=18
	// E1 jumps 0 -> 2 at t=1, skipping phase 1 in cycle 0.
	if err := x.RequestEmergency("E1", 2, 1); err != nil {
		t.Fatal(err)
	}
	// Cycle 1 starts at the wrap at 14; at t=20 we are in phase 0 of cycle 1.
	// Jumping 0 -> 2 would skip phase 1 again in cycle 1: rejected.
	expectErr(t, x.RequestEmergency("E2", 2, 20), ErrConsecutiveSkip, "E2")
	if _, ok := x.RequestState("E2", 20); ok {
		t.Fatal("rejected E2 must not be recorded")
	}
	// The rejection must not advance the clock: an op at t=5 is still legal.
	if err := x.RequestBus("B1", BusExtend, 2, 5); err != nil {
		t.Fatalf("op after rejection: %v", err)
	}
	expectState(t, x, "B1", 5, StatePreempted) // E1 is serving, bus is overridden
}

// Bus extension and an emergency request at the same instant: emergency
// wins and the bus request ends up preempted, not completed.
func TestBusAndEmergencySameTime(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 20, Clearance: 2}, {MinGreen: 5, MaxGreen: 20, Clearance: 2}}
	plan := Plan{Greens: []int{10, 10}, Offset: 0, MaxAdjustPerCycle: 4} // C=24

	// Bus first, then emergency at the same timestamp.
	x := mustBuild(t, specs, plan)
	if err := x.RequestBus("B1", BusExtend, 5, 3); err != nil {
		t.Fatal(err)
	}
	if err := x.RequestEmergency("E1", 1, 3); err != nil {
		t.Fatal(err)
	}
	expectState(t, x, "B1", 3, StatePreempted)
	expectQuery(t, x, 6, QueryResult{Phase: 0, Elapsed: 6, Remaining: 1})
	expectQuery(t, x, 8, QueryResult{Phase: 1, Elapsed: 1, Remaining: 21})

	// Emergency first, then bus at the same timestamp.
	y := mustBuild(t, specs, plan)
	if err := y.RequestEmergency("E1", 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := y.RequestBus("B1", BusExtend, 5, 3); err != nil {
		t.Fatal(err)
	}
	expectState(t, y, "B1", 3, StatePreempted)
}

// A deviation of exactly half a cycle resyncs by lengthening.
func TestHalfCycleDeviationLengthens(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 10, MaxGreen: 22, Clearance: 2}, {MinGreen: 10, MaxGreen: 10, Clearance: 2}}
	x := mustBuild(t, specs, Plan{Greens: []int{10, 10}, Offset: 0, MaxAdjustPerCycle: 5}) // C=24
	// Extend phase 0 to its max 22: the next cycle starts at 36, i.e. 12 = C/2 late.
	if err := x.RequestEmergency("E1", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := x.ConfirmPassage("none", 40); err != nil { // no-op, commits the wrap at 36
		t.Fatal(err)
	}
	if x.e.devR != 12 {
		t.Fatalf("devR = %d, want 12 (half cycle)", x.e.devR)
	}
	if x.e.greens[0] != 15 {
		t.Fatalf("greens[0] = %d, want 15: half-cycle tie must lengthen", x.e.greens[0])
	}
	expectQuery(t, x, 37, QueryResult{Phase: 0, Elapsed: 1, Remaining: 16, Deviation: 12})
	// Resync converges: wraps at 65, 94, 120; deviation back to 0.
	if err := x.ConfirmPassage("none", 130); err != nil {
		t.Fatal(err)
	}
	expectQuery(t, x, 121, QueryResult{Phase: 0, Elapsed: 1, Remaining: 11, Deviation: 0})
}

// A second priority service during resync re-determines the deviation from
// the actual state after the interruption.
func TestResyncInterrupted(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 15, Clearance: 1}, {MinGreen: 10, MaxGreen: 30, Clearance: 1}, {MinGreen: 5, MaxGreen: 15, Clearance: 1}}
	x := mustBuild(t, specs, Plan{Greens: []int{10, 20, 10}, Offset: 0, MaxAdjustPerCycle: 4}) // C=43
	// E1 extends phase 0 to 15: wrap at 48 (devR 5, shorten 4), wrap at 87 (devR 1, shorten 1).
	if err := x.RequestEmergency("E1", 0, 1); err != nil {
		t.Fatal(err)
	}
	// E2 interrupts at t=100 (phase 1), jumping back to phase 0; the jump
	// crosses the cycle boundary at 108, re-measuring the deviation.
	if err := x.RequestEmergency("E2", 0, 100); err != nil {
		t.Fatal(err)
	}
	if err := x.ConfirmPassage("none", 110); err != nil { // no-op, commits the wrap at 108
		t.Fatal(err)
	}
	if x.e.devR != 22 {
		t.Fatalf("devR = %d, want 22 (re-determined after interruption)", x.e.devR)
	}
	if x.e.greens[0] != 14 {
		t.Fatalf("greens[0] = %d, want 14 (lengthen by 4)", x.e.greens[0])
	}
	expectQuery(t, x, 110, QueryResult{Phase: 0, Elapsed: 2, Remaining: 14, Deviation: -21})
}

// Queued emergency requests are served in request-time order, ties broken
// by request ID.
func TestEmergencyQueueOrder(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}}
	x := mustBuild(t, specs, Plan{Greens: []int{5, 5, 5}, Offset: 0}) // C=18
	if err := x.RequestEmergency("E1", 2, 1); err != nil {            // serving until 13
		t.Fatal(err)
	}
	if err := x.RequestEmergency("E9", 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := x.RequestEmergency("E3", 1, 2); err != nil { // same time, smaller ID first
		t.Fatal(err)
	}
	expectErr(t, x.RequestEmergency("EX", 2, 2), ErrTargetOccupied, "same target as serving E1")
	expectState(t, x, "E3", 14, StateServing)
	expectState(t, x, "E9", 14, StateQueued)
	expectState(t, x, "E9", 26, StateServing)
	expectState(t, x, "E1", 26, StateCompleted)
	expectState(t, x, "E3", 26, StateCompleted)
}

// Clock rollback and every rejection class leave no trace on state, queues,
// or the operation clock.
func TestClockRollbackAndRejectionPurity(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 20, Clearance: 2}, {MinGreen: 5, MaxGreen: 20, Clearance: 2}}
	x := mustBuild(t, specs, Plan{Greens: []int{10, 10}, Offset: 0, MaxAdjustPerCycle: 4})
	if err := x.RequestBus("B1", BusExtend, 3, 9); err != nil {
		t.Fatal(err)
	}
	if err := x.RequestEmergency("E1", 1, 10); err != nil {
		t.Fatal(err)
	}
	want := x.Query(20)

	expectErr(t, x.RequestBus("B2", BusExtend, 3, 9), ErrClockRollback, "rollback")
	expectErr(t, x.RequestEmergency("E1", 0, 11), ErrDuplicateRequest, "duplicate")
	expectErr(t, x.RequestEmergency("E2", 5, 11), ErrPhaseNotFound, "bad phase")
	expectErr(t, x.RequestEmergency("", 0, 11), ErrInvalidParam, "empty id")
	expectErr(t, x.ChangePlan(Plan{Greens: []int{1, 1}}, 11), ErrInfeasiblePlan, "infeasible")
	// Precedence: clock rollback beats phase/duplicate; phase beats duplicate.
	expectErr(t, x.RequestEmergency("E1", 5, 9), ErrClockRollback, "rollback precedence")
	expectErr(t, x.RequestEmergency("E1", 5, 12), ErrPhaseNotFound, "phase precedence")

	if _, ok := x.RequestState("E2", 12); ok {
		t.Fatal("rejected E2 must not be recorded")
	}
	if got := x.Query(20); got != want {
		t.Fatalf("state changed by rejections: got %+v, want %+v", got, want)
	}
	// Clock was not advanced by rejections: t=11 is still acceptable.
	if err := x.RequestBus("B3", BusShorten, 2, 11); err != nil {
		t.Fatalf("clock advanced by rejection: %v", err)
	}
}

// Query cost does not grow with the number of elapsed cycles: advancing a
// clean engine by a billion seconds processes a bounded number of
// transitions because whole cycles are skipped in O(1).
func TestQueryCostIndependentOfCycles(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 5, MaxGreen: 15, Clearance: 1}, {MinGreen: 10, MaxGreen: 30, Clearance: 1}, {MinGreen: 5, MaxGreen: 15, Clearance: 1}}
	plan := Plan{Greens: []int{10, 20, 10}, Offset: 0, MaxAdjustPerCycle: 4} // C=43

	e, err := newEngine(specs, plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.advanceTo(1_000_000_000)
	if e.steps > 100 {
		t.Fatalf("clean advance processed %d transitions, want bounded", e.steps)
	}

	// Same with an active deviation being resynced.
	x := mustBuild(t, specs, plan)
	if err := x.RequestEmergency("E1", 0, 1); err != nil {
		t.Fatal(err)
	}
	before := x.e.steps
	x.e.advanceTo(1_000_000_000)
	if got := x.e.steps - before; got > 100 {
		t.Fatalf("resync advance processed %d transitions, want bounded", got)
	}
	// Hand-computed: resync finishes at 129; cycles of 43 afterwards.
	expectQuery(t, x, 1_000_000_000, QueryResult{Phase: 2, Elapsed: 9, Remaining: 2, Deviation: 0})
}
