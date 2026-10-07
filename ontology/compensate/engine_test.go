package compensate

import (
	"strings"
	"sync"
	"testing"
)

func attrsOf(g *Graph, id InstanceID) Attrs { return g.SnapshotAttrs(id) }

func demoGraph() *Graph {
	g := NewGraph()
	g.AddObject(Object{ID: "a", Attrs: Attrs{"x": 1}, Version: 1})
	g.AddObject(Object{ID: "b", Attrs: Attrs{"x": 2}, Version: 1})
	g.AddObject(Object{ID: "c", Attrs: Attrs{"x": 3}, Version: 1})
	g.AddObject(Object{ID: "d", Attrs: Attrs{"x": 4}, Version: 1})
	g.AddLink(Link{ID: "L1", Source: "a", Target: "b", Type: "knows"})
	return g
}

func assertTrace(t *testing.T, tr *SliceTracer, want []string) {
	t.Helper()
	var got []string
	for _, ev := range tr.Events {
		got = append(got, ev.Outcome)
	}
	if len(got) != len(want) {
		t.Fatalf("trace outcomes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace outcomes = %v, want %v", got, want)
		}
	}
}

// TestSingleOpFailure: one op fails, the single prior effect is undone.
func TestSingleOpFailure(t *testing.T) {
	g := demoGraph()
	tr := &SliceTracer{}
	e := NewEngine(g).WithTracer(tr)
	r := e.Execute(Action{Name: "A1", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 100}},
		{Kind: OpValidate, Object: "missing"},
	}})
	if r.Committed || r.Primary != ReasonBusinessReject || r.FailedOpIndex != 1 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if got := attrsOf(g, "a")["x"]; got != 1 {
		t.Fatalf("a.x = %v, want 1 after compensation", got)
	}
	if g.SnapshotVersion("a") != 1 {
		t.Fatalf("a version = %d, want restored 1", g.SnapshotVersion("a"))
	}
	if g.IsPolluted("a") {
		t.Fatalf("a must not be polluted on fully successful compensation")
	}
	assertTrace(t, tr, []string{"effect", "reject", "undo", "verdict"})
}

// TestFailureAtEveryPosition: failure at first/middle/last position.
func TestFailureAtEveryPosition(t *testing.T) {
	for _, failAt := range []int{0, 1, 2, 3} {
		g := demoGraph()
		ops := []SubOp{
			{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 10}},
			{Kind: OpCreateLink, Link: Link{ID: "L2", Source: "a", Target: "b"}},
			{Kind: OpDeleteLink, Link: Link{ID: "L1"}},
			{Kind: OpSetAttrs, Object: "c", Attrs: Attrs{"x": 30}},
		}
		inject := &FaultPlan{Apply: map[int]FaultPoint{failAt: FaultApplyFail}}
		r := NewEngine(g).Execute(Action{Name: "pos", Ops: ops, Inject: inject})
		if r.Committed || r.FailedOpIndex != failAt {
			t.Fatalf("failAt=%d: %+v", failAt, r)
		}
		if attrsOf(g, "a")["x"] != 1 {
			t.Fatalf("failAt=%d a.x=%v", failAt, attrsOf(g, "a")["x"])
		}
		if g.HasLink("L2") {
			t.Fatalf("failAt=%d L2 should be absent", failAt)
		}
		if !g.HasLink("L1") {
			t.Fatalf("failAt=%d L1 should exist", failAt)
		}
		if attrsOf(g, "c")["x"] != 3 {
			t.Fatalf("failAt=%d c.x=%v, failed/later ops must not mutate",
				failAt, attrsOf(g, "c")["x"])
		}
	}
}

// TestCompensationInverseFailure: an undo fails; other inverses still run,
// failures aggregate, involved instances become polluted.
func TestCompensationInverseFailure(t *testing.T) {
	g := demoGraph()
	inject := &FaultPlan{
		Apply: map[int]FaultPoint{3: FaultApplyFail},
		Undo:  map[int]FaultPoint{1: FaultUndoFail},
	}
	ops := []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 10}},
		{Kind: OpCreateLink, Link: Link{ID: "L2", Source: "a", Target: "b"}},
		{Kind: OpSetAttrs, Object: "b", Attrs: Attrs{"x": 20}},
		{Kind: OpValidate, Object: "c"},
	}
	r := NewEngine(g).Execute(Action{Name: "compfail", Ops: ops, Inject: inject})
	if r.Committed || len(r.CompFailures) != 1 {
		t.Fatalf("want exactly one comp failure, got %+v", r)
	}
	if r.CompFailures[0].OpIndex != 1 ||
		r.CompFailures[0].Class != ReasonCompensationFailed {
		t.Fatalf("bad comp failure: %+v", r.CompFailures[0])
	}
	if attrsOf(g, "a")["x"] != 1 {
		t.Fatalf("a.x=%v want 1", attrsOf(g, "a")["x"])
	}
	if attrsOf(g, "b")["x"] != 2 {
		t.Fatalf("b.x=%v want 2", attrsOf(g, "b")["x"])
	}
	if !g.HasLink("L2") {
		t.Fatalf("L2 must remain: its inverse failed")
	}
	if !g.IsPolluted("a") || !g.IsPolluted("b") {
		t.Fatalf("endpoints of the failed link inverse must be polluted: a=%v b=%v",
			g.IsPolluted("a"), g.IsPolluted("b"))
	}
	if r.Primary != ReasonBusinessReject {
		t.Fatalf("primary = %s, business reject outranks compensation failure",
			r.Primary)
	}
}

// TestCompensationInversePanic: a runtime panic inside an inverse is
// converted to a compensation failure and never escapes the flow.
func TestCompensationInversePanic(t *testing.T) {
	g := demoGraph()
	inject := &FaultPlan{
		Apply: map[int]FaultPoint{2: FaultApplyFail},
		Undo:  map[int]FaultPoint{0: FaultUndoPanic},
	}
	ops := []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 10}},
		{Kind: OpSetAttrs, Object: "b", Attrs: Attrs{"x": 20}},
		{Kind: OpValidate, Object: "missing"},
	}
	r := NewEngine(g).Execute(Action{Name: "panicundo", Ops: ops, Inject: inject})
	if len(r.CompFailures) != 1 ||
		!strings.Contains(r.CompFailures[0].Detail, "undo panic") {
		t.Fatalf("want panic-derived comp failure, got %+v", r.CompFailures)
	}
	if attrsOf(g, "b")["x"] != 2 {
		t.Fatalf("b.x=%v want 2 (compensation continues)", attrsOf(g, "b")["x"])
	}
	if attrsOf(g, "a")["x"] != 10 {
		t.Fatalf("a.x=%v want 10 (panicked inverse made no change)",
			attrsOf(g, "a")["x"])
	}
}

// TestPollutedRejectsBeforeAnyEffect: contamination rejection precedes all
// sub-operations; versions of all involved instances stay untouched.
func TestPollutedRejectsBeforeAnyEffect(t *testing.T) {
	g := demoGraph()
	e := NewEngine(g)
	g.markPolluted("b", 5)
	beforeA := g.SnapshotVersion("a")
	r := e.Execute(Action{Name: "polluted", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 999}},
		{Kind: OpSetAttrs, Object: "b", Attrs: Attrs{"x": 888}},
	}})
	if r.Committed || r.Primary != ReasonContaminated {
		t.Fatalf("want contaminated reject, got %+v", r)
	}
	if attrsOf(g, "a")["x"] != 1 {
		t.Fatalf("a.x mutated despite pre-execution rejection")
	}
	if g.SnapshotVersion("a") != beforeA {
		t.Fatalf("a version bumped by rejected action")
	}
	e.Repair("b")
	r = e.Execute(Action{Name: "repaired", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 999}},
	}})
	if !r.Committed {
		t.Fatalf("want commit after repair, got %+v", r)
	}
}

// TestEarliestPollutionIndexSurvivesReContamination.
func TestEarliestPollutionIndexSurvivesReContamination(t *testing.T) {
	g := demoGraph()
	g.markPolluted("a", 5)
	g.markPolluted("a", 2)
	g.markPolluted("a", 9)
	idx, ok := g.PollutionIndex("a")
	if !ok || idx != 2 {
		t.Fatalf("earliest index = %d ok=%v, want 2", idx, ok)
	}

	g2 := demoGraph()
	e := NewEngine(g2)
	r1 := e.Execute(Action{Name: "first", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 11}},
		{Kind: OpSetAttrs, Object: "b", Attrs: Attrs{"x": 22}},
		{Kind: OpValidate, Object: "missing"},
	}, Inject: &FaultPlan{Undo: map[int]FaultPoint{1: FaultUndoFail}}})
	if len(r1.CompFailures) != 1 {
		t.Fatalf("first: %+v", r1)
	}
	idx1, ok1 := g2.PollutionIndex("b")
	if !ok1 || idx1 != 1 {
		t.Fatalf("first pollution index for b = %d ok=%v, want 1", idx1, ok1)
	}
	if g2.IsPolluted("a") {
		t.Fatalf("a was not part of the failing inverse, must stay clean")
	}
	// New actions on polluted instances are rejected pre-execution; repair
	// then re-contaminate in a later episode, and verify the new episode's
	// earliest failing number is tracked on its own.
	e.Execute(Action{Name: "second", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 33}},
		{Kind: OpValidate, Object: "missing"},
	}, Inject: &FaultPlan{Undo: map[int]FaultPoint{0: FaultUndoFail}}})
	idx2, _ := g2.PollutionIndex("a")
	if idx2 != 0 {
		t.Fatalf("post-repair pollution index = %d, want 0", idx2)
	}
}

// TestConcurrentDisjointSetsSerialEquivalence.
func TestConcurrentDisjointSetsSerialEquivalence(t *testing.T) {
	g := demoGraph()
	var wg sync.WaitGroup
	wg.Add(2)
	var rA, rB ActionResult
	go func() {
		defer wg.Done()
		rA = NewEngine(g).Execute(Action{Name: "left", Ops: []SubOp{
			{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 100}},
		}})
	}()
	go func() {
		defer wg.Done()
		rB = NewEngine(g).Execute(Action{Name: "right", Ops: []SubOp{
			{Kind: OpSetAttrs, Object: "c", Attrs: Attrs{"x": 300}},
		}})
	}()
	wg.Wait()
	if !rA.Committed || !rB.Committed {
		t.Fatalf("disjoint actions must both commit: %+v %+v", rA, rB)
	}
	if attrsOf(g, "a")["x"] != 100 || attrsOf(g, "c")["x"] != 300 {
		t.Fatalf("state matches no serial order: a=%v c=%v",
			attrsOf(g, "a")["x"], attrsOf(g, "c")["x"])
	}
}

// TestConcurrentContentionRejectsBeforeMutation.
func TestConcurrentContentionRejectsBeforeMutation(t *testing.T) {
	g := demoGraph()
	if !g.tryAcquire([]string{tokenObj("a")}) {
		t.Fatal("setup failed")
	}
	defer g.release([]string{tokenObj("a")})
	beforeA := g.SnapshotVersion("a")
	beforeC := g.SnapshotVersion("c")
	r := NewEngine(g).Execute(Action{Name: "loser", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 1}},
		{Kind: OpSetAttrs, Object: "c", Attrs: Attrs{"x": 9}},
	}})
	if r.Committed || r.Primary != ReasonContention {
		t.Fatalf("want contention reject, got %+v", r)
	}
	if g.SnapshotVersion("a") != beforeA || g.SnapshotVersion("c") != beforeC {
		t.Fatalf("rejected action changed versions: a=%d c=%d",
			g.SnapshotVersion("a"), g.SnapshotVersion("c"))
	}
	if attrsOf(g, "a")["x"] != 1 || attrsOf(g, "c")["x"] != 3 {
		t.Fatalf("rejected action mutated attributes")
	}
}

// TestContentionOnSameLink.
func TestContentionOnSameLink(t *testing.T) {
	g := demoGraph()
	tok := tokenLink("L1")
	if !g.tryAcquire([]string{tok}) {
		t.Fatal("setup failed")
	}
	defer g.release([]string{tok})
	r := NewEngine(g).Execute(Action{Name: "linkrace", Ops: []SubOp{
		{Kind: OpDeleteLink, Link: Link{ID: "L1", Source: "a", Target: "b"}},
	}})
	if r.Primary != ReasonContention || r.Committed {
		t.Fatalf("want contention, got %+v", r)
	}
	if !g.HasLink("L1") {
		t.Fatalf("link deleted by rejected action")
	}
}

// TestPriorityOrder: contamination outranks business and contention;
// business outranks contention; all independent of compensation outcome.
func TestPriorityOrder(t *testing.T) {
	if HigherReason(ReasonContaminated, ReasonBusinessReject) != ReasonContaminated ||
		HigherReason(ReasonBusinessReject, ReasonContention) != ReasonBusinessReject ||
		HigherReason(ReasonContention, ReasonCompensationFailed) != ReasonContention {
		t.Fatal("fixed priority broken")
	}

	g := demoGraph()
	g.markPolluted("a", 0)
	// Even with a busy resource AND an invalid op, contamination wins.
	if !g.tryAcquire([]string{tokenObj("a"), tokenObj("c")}) {
		t.Fatal("setup failed")
	}
	r := NewEngine(g).Execute(Action{Name: "prio", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 1}},
		{Kind: OpValidate, Object: "missing"},
	}})
	if r.Primary != ReasonContaminated {
		t.Fatalf("primary = %s, want contaminated", r.Primary)
	}
	g.release([]string{tokenObj("a"), tokenObj("c")})

	// repaired: contention now outranks the would-be business failure
	g.Repair("a")
	if !g.tryAcquire([]string{tokenObj("a")}) {
		t.Fatal("setup failed")
	}
	defer g.release([]string{tokenObj("a")})
	r = NewEngine(g).Execute(Action{Name: "prio2", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 1}},
		{Kind: OpValidate, Object: "missing"},
	}})
	if r.Primary != ReasonContention {
		t.Fatalf("primary = %s, want contention", r.Primary)
	}
}

// TestRegistrationNumbersNoDuplicate: effect+register indivisibility implies
// each effective op gets one unique monotonic registration number.
func TestRegistrationNumbersNoDuplicate(t *testing.T) {
	g := demoGraph()
	tr := &SliceTracer{}
	e := NewEngine(g).WithTracer(tr)
	e.Execute(Action{Name: "seq", Ops: []SubOp{
		{Kind: OpSetAttrs, Object: "a", Attrs: Attrs{"x": 10}},
		{Kind: OpSetAttrs, Object: "b", Attrs: Attrs{"x": 20}},
		{Kind: OpValidate, Object: "c"},
	}})
	seen := map[string]bool{}
	var regs []string
	for _, ev := range tr.Events {
		if ev.Outcome == "effect" {
			if seen[ev.Detail] {
				t.Fatalf("duplicate registration %s", ev.Detail)
			}
			seen[ev.Detail] = true
			regs = append(regs, ev.Detail)
		}
	}
	if len(regs) != 3 {
		t.Fatalf("want 3 registrations, got %v", regs)
	}
}
