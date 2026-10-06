package ontology

import (
	"strings"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if got := KindOf(err); got != want {
		t.Fatalf("error kind = %q, want %q (err=%v)", got, want, err)
	}
}

func newBasicEngine(t *testing.T) *Engine {
	e := New()
	for _, g := range []string{"g1", "g2", "g3"} {
		must(t, e.AddGroup(Group{ID: g}))
	}
	must(t, e.AddQuestion(Question{ID: "q", MaxScore: 10, Step: 2, Threshold: 2}))
	must(t, e.AddQuestion(Question{ID: "q1", MaxScore: 10, Step: 1, Threshold: 2}))
	return e
}

func addRev(t *testing.T, e *Engine, id, group string, quota int, avoids ...string) {
	t.Helper()
	must(t, e.AddReviewer(Reviewer{ID: id, GroupID: group, Quota: quota, Active: true}, avoids))
}

func taskFor(v View, sheet, reviewer string) TaskView {
	for _, s := range v.Sheets {
		if s.ID != sheet {
			continue
		}
		for _, tk := range s.Tasks {
			if tk.ReviewerID == reviewer && !tk.Withdrawn {
				return tk
			}
		}
	}
	return TaskView{}
}

func finalOf(v View, sheet string) *int {
	for _, s := range v.Sheets {
		if s.ID == sheet {
			return s.FinalScore
		}
	}
	return nil
}

func TestGapEqualsThresholdIsAgreement(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r3", "g3", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q1", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 4))
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 6)) // gap == threshold 2
	if f := finalOf(e.Snapshot(), "s"); f == nil || *f != 5 {
		t.Fatalf("final = %v, want average 5", f)
	}
	for _, ev := range e.Snapshot().Events {
		if ev.Kind == EventArbitration {
			t.Fatalf("arbitration must not trigger at gap==threshold")
		}
	}
}

func TestAverageSnapsUpTowardMax(t *testing.T) {
	e := newBasicEngine(t) // step 2
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 4))
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 6)) // avg 5 -> grid up 6
	if f := *finalOf(e.Snapshot(), "s"); f != 6 {
		t.Fatalf("final = %d, want 6 (snapped toward max)", f)
	}
}

func TestArbitratorEquidistantPicksHigher(t *testing.T) {
	e := newBasicEngine(t) // threshold 2, step 2
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r3", "g3", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 2))
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 8)) // gap 6 > 2
	arb := taskFor(e.Snapshot(), "s", "r3")
	if arb.Role != "arbitrator" {
		t.Fatalf("r3 role = %q, want arbitrator", arb.Role)
	}
	must(t, e.Submit(arb.ID, 6))                  // equidistant (4) to 2 and 8; use higher 8
	if f := *finalOf(e.Snapshot(), "s"); f != 8 { // avg 7 -> grid up 8
		t.Fatalf("final = %d, want 8", f)
	}
	var basis string
	for _, ev := range e.Snapshot().Events {
		if ev.Kind == EventFinal {
			basis = ev.Basis
		}
	}
	if !strings.Contains(basis, "closer_side=r2") {
		t.Fatalf("final basis %q must name higher side r2", basis)
	}
}

func TestArbitratorFarFromBoth(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r3", "g3", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 0))
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 4)) // gap 4 > 2
	arb := taskFor(e.Snapshot(), "s", "r3")
	must(t, e.Submit(arb.ID, 10)) // d to 0 =10, to 4 =6, both > 2
	if f := *finalOf(e.Snapshot(), "s"); f != 10 {
		t.Fatalf("final = %d, want arbitrator score 10", f)
	}
}

func TestWithdrawExcludesReviewerAndKeepsScore(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r3", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 4))
	must(t, e.Withdraw(taskFor(v, "s", "r2").ID))
	rep := taskFor(e.Snapshot(), "s", "r3")
	if rep.ID == "" || rep.Role != "initial" {
		t.Fatalf("expected r3 replacement initial task, got %+v", rep)
	}
	for _, tk := range e.Snapshot().Sheets[0].Tasks {
		if tk.ReviewerID == "r2" && !tk.Withdrawn {
			t.Fatalf("withdrawn r2 was reselected")
		}
	}
	must(t, e.Submit(rep.ID, 4))
	if f := *finalOf(e.Snapshot(), "s"); f != 4 {
		t.Fatalf("final = %d, want 4; earlier score retained", f)
	}
}

func TestDeactivateKeepsSubmittedScores(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r4", "g1", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 6)) // r1 finished s
	must(t, e.AddSheet(Sheet{ID: "s2", Question: "q", Student: "st2"}))
	open := taskFor(e.Snapshot(), "s2", "r1")
	if open.ID == "" {
		t.Fatalf("expected r1 to hold an open task on s2")
	}
	must(t, e.Deactivate("r1"))
	rep := taskFor(e.Snapshot(), "s2", "r4")
	if rep.ID == "" {
		t.Fatalf("group-mate r4 should take r1's open task")
	}
	v = e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 6))
	if f := finalOf(e.Snapshot(), "s"); f == nil || *f != 6 {
		t.Fatalf("s final = %v, want 6 retained after deactivation", f)
	}
}

func TestQuotaExactlyFull(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 1)
	addRev(t, e, "r2", "g2", 5)
	addRev(t, e, "r4", "g1", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	if taskFor(e.Snapshot(), "s", "r1").ID == "" {
		t.Fatalf("r1 should receive its single task")
	}
	must(t, e.AddSheet(Sheet{ID: "s2", Question: "q", Student: "st2"}))
	v := e.Snapshot()
	if taskFor(v, "s2", "r1").ID != "" {
		t.Fatalf("full-quota r1 must not get a second task")
	}
	if taskFor(v, "s2", "r4").ID == "" {
		t.Fatalf("r4 should take the g1 slot on s2")
	}
}

func TestAvoidAndPairExhaustGroupThenNewReviewer(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5, "st") // avoids st
	addRev(t, e, "r4", "g1", 5)       // will be paired with st
	addRev(t, e, "r2", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "pre", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "pre", "r4").ID, 4))
	must(t, e.Submit(taskFor(v, "pre", "r2").ID, 4))
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	var pending bool
	for _, s := range e.Snapshot().Sheets {
		if s.ID == "s" {
			pending = s.PendingTask
		}
	}
	if !pending {
		t.Fatalf("g1 exhausted by avoid(r1)+paired(r4); s must be pending")
	}
	addRev(t, e, "r9", "g1", 5)
	e.TriggerAllocation()
	if taskFor(e.Snapshot(), "s", "r9").ID == "" {
		t.Fatalf("new reviewer r9 must claim the pending task after trigger")
	}
}

func TestRejectionPriority(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	task := taskFor(v, "s", "r1").ID

	// invalid argument outranks every other fault
	assertKind(t, e.Submit("", 0), ErrInvalidArgument)
	assertKind(t, e.Submit("", 99), ErrInvalidArgument)
	// missing task outranks deactivated/state/score faults
	assertKind(t, e.Submit("nope", -1), ErrNotFound)
	// deactivate r1; deactivated outranks state and score faults
	r1task := task
	must(t, e.Deactivate("r1"))
	assertKind(t, e.Submit(r1task, 99), ErrTaskState) // task withdrawn+reassigned
	assertKind(t, e.Withdraw(r1task), ErrTaskState)
	assertKind(t, e.Deactivate("r1"), ErrDeactivated)
	// active reviewer, then state faults outrank score faults
	task2 := taskFor(e.Snapshot(), "s", "r2").ID
	must(t, e.Submit(task2, 4))
	assertKind(t, e.Submit(task2, 99), ErrTaskState) // duplicate; bad score irrelevant
	// off-grid/out-of-range score reported when state is otherwise fine
	must(t, e.AddSheet(Sheet{ID: "s2", Question: "q", Student: "st2"}))
	open := taskFor(e.Snapshot(), "s2", "r2").ID
	assertKind(t, e.Submit(open, 3), ErrScore) // not on step-2 grid
	assertKind(t, e.Submit(open, 12), ErrScore)
	assertKind(t, e.Submit(open, -2), ErrScore)
	// withdraw validation priority
	assertKind(t, e.Withdraw(""), ErrInvalidArgument)
	assertKind(t, e.Withdraw("nope"), ErrNotFound)
	assertKind(t, e.Withdraw(task2), ErrTaskState) // already submitted
	// deactivate validation priority
	assertKind(t, e.Deactivate(""), ErrInvalidArgument)
	assertKind(t, e.Deactivate("ghost"), ErrNotFound)
}

func TestFinalizedSheetRejectsAll(t *testing.T) {
	e := newBasicEngine(t)
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	a := taskFor(v, "s", "r1").ID
	b := taskFor(v, "s", "r2").ID
	must(t, e.Submit(a, 4))
	must(t, e.Submit(b, 4))
	assertKind(t, e.Submit(a, 4), ErrTaskState)
	assertKind(t, e.Withdraw(b), ErrTaskState)
}

func TestPendingArbitratorClaimedAfterThirdGroupAdded(t *testing.T) {
	e := New()
	for _, g := range []string{"g1", "g2"} {
		must(t, e.AddGroup(Group{ID: g}))
	}
	must(t, e.AddQuestion(Question{ID: "q", MaxScore: 10, Step: 1, Threshold: 1}))
	addRev(t, e, "r1", "g1", 5)
	addRev(t, e, "r2", "g2", 5)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	v := e.Snapshot()
	must(t, e.Submit(taskFor(v, "s", "r1").ID, 0))
	must(t, e.Submit(taskFor(v, "s", "r2").ID, 9)) // gap 9 > 1, no third group
	var sv SheetView
	for _, x := range e.Snapshot().Sheets {
		if x.ID == "s" {
			sv = x
		}
	}
	if !sv.PendingTask {
		t.Fatalf("arbitration task must be pending without a third group")
	}
	if sv.FinalScore != nil {
		t.Fatalf("sheet must not be finalized while arbitrator pending")
	}
	must(t, e.AddGroup(Group{ID: "g3"}))
	addRev(t, e, "r3", "g3", 5) // AddReviewer triggers pending allocation
	arb := taskFor(e.Snapshot(), "s", "r3")
	if arb.Role != "arbitrator" {
		t.Fatalf("r3 should pick up the pending arbitration, got %+v", arb)
	}
	must(t, e.Submit(arb.ID, 9))
	if f := *finalOf(e.Snapshot(), "s"); f != 9 {
		t.Fatalf("final = %d, want 9", f)
	}
}
