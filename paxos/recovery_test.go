package paxos

import (
	"errors"
	"reflect"
	"testing"
)

func report(pb, chosen int, accepted map[int]AcceptedValue) PromiseReport {
	return PromiseReport{PB: pb, Chosen: chosen, Accepted: accepted}
}

func mustPlanner(t *testing.T, b, n int) *Planner {
	t.Helper()
	p, err := NewPlanner(b, n)
	if err != nil {
		t.Fatalf("NewPlanner(%d, %d) failed: %v", b, n, err)
	}
	return p
}

func mustAddPromise(t *testing.T, p *Planner, from int, rep PromiseReport) {
	t.Helper()
	if err := p.AddPromise(from, rep); err != nil {
		t.Fatalf("AddPromise(%d) failed: %v", from, err)
	}
}

func TestNewPlannerRejectsNonPositive(t *testing.T) {
	for _, tc := range [][2]int{{0, 3}, {-1, 3}, {5, 0}, {5, -2}, {0, 0}} {
		if _, err := NewPlanner(tc[0], tc[1]); err == nil {
			t.Fatalf("NewPlanner(%d, %d) unexpectedly accepted", tc[0], tc[1])
		}
	}
	if _, err := NewPlanner(1, 1); err != nil {
		t.Fatalf("NewPlanner(1, 1) failed: %v", err)
	}
}

// A single accept at ballot 5 must win over two identical accepts at
// ballot 3: values are picked by highest ballot, not by majority count.
func TestHighestBallotWinsOverMajorityCount(t *testing.T) {
	p := mustPlanner(t, 10, 3)
	mustAddPromise(t, p, 0, report(10, 0, map[int]AcceptedValue{5: {Ballot: 5, Value: "x"}}))
	mustAddPromise(t, p, 1, report(10, 0, map[int]AcceptedValue{5: {Ballot: 3, Value: "y"}}))
	mustAddPromise(t, p, 2, report(10, 0, map[int]AcceptedValue{5: {Ballot: 3, Value: "y"}}))

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if plan.Start != 1 || plan.NextFree != 6 {
		t.Fatalf("got start=%d nextFree=%d, want 1 and 6", plan.Start, plan.NextFree)
	}
	last := plan.Entries[len(plan.Entries)-1]
	if last.Slot != 5 || last.Noop || last.Value != "x" || last.Ballot != 5 {
		t.Fatalf("slot %+v, want slot 5 value x ballot 5", last)
	}
	for _, e := range plan.Entries[:len(plan.Entries)-1] {
		if !e.Noop || e.Ballot != 0 {
			t.Fatalf("hole slot %+v, want Noop with ballot 0", e)
		}
	}
}

// Accepts only at slots 2 and 4: slot 3 must be filled with a Noop,
// and an accepted empty-string value must stay distinguishable from a
// Noop.
func TestNoopFillsHolesAndEmptyStringIsNotNoop(t *testing.T) {
	p := mustPlanner(t, 5, 1)
	mustAddPromise(t, p, 0, report(5, 1, map[int]AcceptedValue{
		2: {Ballot: 2, Value: ""},
		4: {Ballot: 3, Value: "v"},
	}))

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	want := []PlanEntry{
		{Slot: 2, Noop: false, Value: "", Ballot: 2},
		{Slot: 3, Noop: true, Value: "", Ballot: 0},
		{Slot: 4, Noop: false, Value: "v", Ballot: 3},
	}
	if !reflect.DeepEqual(plan.Entries, want) {
		t.Fatalf("entries %+v, want %+v", plan.Entries, want)
	}
	if plan.Start != 2 || plan.NextFree != 5 {
		t.Fatalf("got start=%d nextFree=%d, want 2 and 5", plan.Start, plan.NextFree)
	}
}

// start is derived from the maximum chosen across reports; accepted
// entries below start are ignored.
func TestStartFromMaxChosenAndBelowStartIgnored(t *testing.T) {
	p := mustPlanner(t, 8, 3)
	mustAddPromise(t, p, 0, report(8, 2, map[int]AcceptedValue{3: {Ballot: 1, Value: "a"}}))
	mustAddPromise(t, p, 1, report(8, 5, map[int]AcceptedValue{6: {Ballot: 1, Value: "b"}}))
	mustAddPromise(t, p, 2, report(8, 1, map[int]AcceptedValue{8: {Ballot: 1, Value: "c"}}))

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if plan.Start != 6 {
		t.Fatalf("start=%d, want 6 (max chosen 5 + 1)", plan.Start)
	}
	want := []PlanEntry{
		{Slot: 6, Noop: false, Value: "b", Ballot: 1},
		{Slot: 7, Noop: true, Value: "", Ballot: 0},
		{Slot: 8, Noop: false, Value: "c", Ballot: 1},
	}
	if !reflect.DeepEqual(plan.Entries, want) {
		t.Fatalf("entries %+v, want %+v", plan.Entries, want)
	}
	if plan.NextFree != 9 {
		t.Fatalf("nextFree=%d, want 9", plan.NextFree)
	}
}

// When maxSlot < start nothing is recovered and nextFree == start.
func TestNextFreeEqualsStartWhenMaxSlotBelowStart(t *testing.T) {
	p := mustPlanner(t, 8, 3)
	mustAddPromise(t, p, 0, report(8, 10, nil))
	mustAddPromise(t, p, 1, report(8, 0, map[int]AcceptedValue{5: {Ballot: 2, Value: "old"}}))
	mustAddPromise(t, p, 2, report(8, 3, nil))

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if plan.Start != 11 {
		t.Fatalf("start=%d, want 11", plan.Start)
	}
	if len(plan.Entries) != 0 {
		t.Fatalf("entries %+v, want none (maxSlot 5 < start 11)", plan.Entries)
	}
	if plan.NextFree != 11 {
		t.Fatalf("nextFree=%d, want 11 (== start)", plan.NextFree)
	}

	// All accepted tables empty: maxSlot is 0.
	q := mustPlanner(t, 8, 1)
	mustAddPromise(t, q, 0, report(8, 4, nil))
	qplan, err := q.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if qplan.Start != 5 || qplan.NextFree != 5 || len(qplan.Entries) != 0 {
		t.Fatalf("got %+v, want start=5 nextFree=5 no entries", qplan)
	}
}

// Same highest ballot with the same value is fine; different values at
// the highest ballot are a conflict, reported at the smallest slot.
func TestConflictOnlyAmongHighestBallotValues(t *testing.T) {
	// Same value at the highest ballot: no conflict.
	p := mustPlanner(t, 10, 3)
	mustAddPromise(t, p, 0, report(10, 0, map[int]AcceptedValue{3: {Ballot: 7, Value: "a"}}))
	mustAddPromise(t, p, 1, report(10, 0, map[int]AcceptedValue{3: {Ballot: 7, Value: "a"}}))
	mustAddPromise(t, p, 2, report(10, 0, nil))
	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if got := plan.Entries[2]; got.Slot != 3 || got.Value != "a" || got.Ballot != 7 || got.Noop {
		t.Fatalf("slot %+v, want slot 3 value a ballot 7", got)
	}

	// Different values at the highest ballot: conflict at the smallest
	// conflicting slot (3, not 5).
	q := mustPlanner(t, 10, 3)
	mustAddPromise(t, q, 0, report(10, 0, map[int]AcceptedValue{
		3: {Ballot: 7, Value: "a"},
		5: {Ballot: 7, Value: "x"},
	}))
	mustAddPromise(t, q, 1, report(10, 0, map[int]AcceptedValue{
		3: {Ballot: 7, Value: "b"},
		5: {Ballot: 7, Value: "y"},
	}))
	_, err = q.Plan()
	var cerr *ConflictError
	if !errors.As(err, &cerr) {
		t.Fatalf("err=%v, want ConflictError", err)
	}
	if cerr.Slot != 3 {
		t.Fatalf("conflict slot=%d, want 3 (smallest)", cerr.Slot)
	}

	// Failed Plan must not close the planner.
	mustAddPromise(t, q, 2, report(10, 0, nil))
	if _, err := q.Plan(); !errors.As(err, &cerr) {
		t.Fatalf("second Plan err=%v, want ConflictError again", err)
	}
}

// Differences below the highest ballot are not conflicts.
func TestLowerBallotDifferencesNotConflict(t *testing.T) {
	p := mustPlanner(t, 10, 3)
	mustAddPromise(t, p, 0, report(10, 0, map[int]AcceptedValue{3: {Ballot: 5, Value: "a"}}))
	mustAddPromise(t, p, 1, report(10, 0, map[int]AcceptedValue{3: {Ballot: 3, Value: "b"}}))
	mustAddPromise(t, p, 2, report(10, 0, map[int]AcceptedValue{3: {Ballot: 1, Value: "c"}}))

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if got := plan.Entries[2]; got.Slot != 3 || got.Value != "a" || got.Ballot != 5 {
		t.Fatalf("slot %+v, want slot 3 value a ballot 5", got)
	}
}

// Majority is exactly floor(n/2)+1 distinct acceptors.
func TestMajorityExactAndOneShort(t *testing.T) {
	p := mustPlanner(t, 4, 4) // majority = 3
	mustAddPromise(t, p, 0, report(4, 0, nil))
	mustAddPromise(t, p, 1, report(4, 0, nil))
	if _, err := p.Plan(); !errors.Is(err, ErrNoMajority) {
		t.Fatalf("err=%v, want ErrNoMajority with 2/4 reports", err)
	}
	// Failed Plan does not close: a third report unblocks it.
	mustAddPromise(t, p, 2, report(4, 0, nil))
	if _, err := p.Plan(); err != nil {
		t.Fatalf("Plan failed with exact majority: %v", err)
	}
}

// AddPromise reports only the first error in this order: closed, from
// out of range, ballot mismatch, accepted entries (ascending slot; slot
// check before ballot check per entry), duplicate.
func TestAddPromiseValidationOrder(t *testing.T) {
	p := mustPlanner(t, 5, 2)

	// from out of range beats ballot mismatch.
	if err := p.AddPromise(2, report(99, 0, nil)); !errors.Is(err, ErrFromOutOfRange) {
		t.Fatalf("err=%v, want ErrFromOutOfRange", err)
	}
	if err := p.AddPromise(-1, report(5, 0, nil)); !errors.Is(err, ErrFromOutOfRange) {
		t.Fatalf("err=%v, want ErrFromOutOfRange", err)
	}
	// Ballot mismatch beats entry errors.
	bad := report(99, 0, map[int]AcceptedValue{0: {Ballot: 0, Value: "x"}})
	if err := p.AddPromise(0, bad); !errors.Is(err, ErrBallotMismatch) {
		t.Fatalf("err=%v, want ErrBallotMismatch", err)
	}
	// Entries are checked in ascending slot order: the slot-2 violation
	// (slot <= chosen) is reported before the slot-4 ballot violation.
	entries := report(5, 3, map[int]AcceptedValue{
		2: {Ballot: 1, Value: "a"},
		4: {Ballot: 0, Value: "b"},
	})
	if err := p.AddPromise(0, entries); !errors.Is(err, ErrSlotNotAfterChosen) {
		t.Fatalf("err=%v, want ErrSlotNotAfterChosen", err)
	}
	// Per entry, the slot check comes before the ballot check: slot 4
	// is <= chosen 5, so the slot error wins over the zero ballot.
	both := report(5, 5, map[int]AcceptedValue{4: {Ballot: 0, Value: "x"}})
	if err := p.AddPromise(0, both); !errors.Is(err, ErrSlotNotAfterChosen) {
		t.Fatalf("err=%v, want ErrSlotNotAfterChosen", err)
	}
	// Slot 0 is never greater than a non-negative chosen.
	zero := report(5, 0, map[int]AcceptedValue{0: {Ballot: 1, Value: "x"}})
	if err := p.AddPromise(0, zero); !errors.Is(err, ErrSlotNotAfterChosen) {
		t.Fatalf("err=%v, want ErrSlotNotAfterChosen for slot 0", err)
	}
	// Accept ballot 0 or >= b is rejected.
	zeroBallot := report(5, 0, map[int]AcceptedValue{1: {Ballot: 0, Value: "x"}})
	if err := p.AddPromise(0, zeroBallot); !errors.Is(err, ErrInvalidAcceptBallot) {
		t.Fatalf("err=%v, want ErrInvalidAcceptBallot", err)
	}
	bigBallot := report(5, 0, map[int]AcceptedValue{1: {Ballot: 5, Value: "x"}})
	if err := p.AddPromise(0, bigBallot); !errors.Is(err, ErrInvalidAcceptBallot) {
		t.Fatalf("err=%v, want ErrInvalidAcceptBallot", err)
	}
	// Entry errors beat duplicate detection.
	mustAddPromise(t, p, 0, report(5, 0, nil))
	if err := p.AddPromise(0, bad); !errors.Is(err, ErrBallotMismatch) {
		t.Fatalf("err=%v, want ErrBallotMismatch (before duplicate)", err)
	}
	if err := p.AddPromise(0, report(5, 0, nil)); !errors.Is(err, ErrDuplicatePromise) {
		t.Fatalf("err=%v, want ErrDuplicatePromise", err)
	}
	// A successful Plan closes the planner; closed beats everything.
	mustAddPromise(t, p, 1, report(5, 0, nil))
	if _, err := p.Plan(); err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if err := p.AddPromise(9, report(99, 0, nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v, want ErrClosed", err)
	}
	if _, err := p.Plan(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Plan err=%v, want ErrClosed", err)
	}
}

// Rejected operations must not change any state.
func TestRejectedOpsDontChangeState(t *testing.T) {
	p := mustPlanner(t, 5, 2)
	mustAddPromise(t, p, 0, report(5, 1, map[int]AcceptedValue{2: {Ballot: 2, Value: "v"}}))

	rejects := []error{
		p.AddPromise(7, report(5, 0, nil)),                                               // from out of range
		p.AddPromise(1, report(6, 0, nil)),                                               // ballot mismatch
		p.AddPromise(1, report(5, 2, map[int]AcceptedValue{2: {Ballot: 1, Value: "x"}})), // slot <= chosen
		p.AddPromise(1, report(5, 0, map[int]AcceptedValue{3: {Ballot: 9, Value: "x"}})), // ballot >= b
		p.AddPromise(0, report(5, 0, nil)),                                               // duplicate
	}
	for i, err := range rejects {
		if err == nil {
			t.Fatalf("reject %d unexpectedly accepted", i)
		}
	}
	if _, err := p.Plan(); !errors.Is(err, ErrNoMajority) {
		t.Fatalf("err=%v, want ErrNoMajority (failed Plan keeps planner open)", err)
	}

	// The same valid report for acceptor 1 must still be accepted and
	// the plan must reflect only the two valid reports.
	mustAddPromise(t, p, 1, report(5, 0, map[int]AcceptedValue{4: {Ballot: 3, Value: "w"}}))
	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	want := []PlanEntry{
		{Slot: 2, Noop: false, Value: "v", Ballot: 2},
		{Slot: 3, Noop: true, Value: "", Ballot: 0},
		{Slot: 4, Noop: false, Value: "w", Ballot: 3},
	}
	if plan.Start != 2 || plan.NextFree != 5 || !reflect.DeepEqual(plan.Entries, want) {
		t.Fatalf("plan %+v, want start=2 nextFree=5 entries %+v", plan, want)
	}
}

// The returned plan must not alias internal state, and reports must be
// copied on arrival.
func TestNoAliasing(t *testing.T) {
	p := mustPlanner(t, 5, 1)
	accepted := map[int]AcceptedValue{2: {Ballot: 1, Value: "v"}}
	mustAddPromise(t, p, 0, report(5, 1, accepted))
	// Mutating the caller's map after AddPromise must not affect Plan.
	accepted[2] = AcceptedValue{Ballot: 1, Value: "corrupted"}
	accepted[3] = AcceptedValue{Ballot: 1, Value: "corrupted"}

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	want := []PlanEntry{{Slot: 2, Noop: false, Value: "v", Ballot: 1}}
	if !reflect.DeepEqual(plan.Entries, want) {
		t.Fatalf("entries %+v, want %+v (report must be copied on arrival)", plan.Entries, want)
	}
}
