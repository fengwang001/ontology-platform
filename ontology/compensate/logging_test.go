package compensate

import (
	"bytes"
	"strings"
	"testing"
)

// TestLoggedWalkthrough prints the input, every effect/undo step and the
// final verdict basis for one action with multi-position compensation,
// satisfying the "print inputs and every step" test requirement.
func TestLoggedWalkthrough(t *testing.T) {
	g := NewGraph()
	g.AddObject(Object{ID: "o1", Attrs: Attrs{"v": 1}, Version: 1})
	g.AddObject(Object{ID: "o2", Attrs: Attrs{"v": 2}, Version: 1})
	g.AddLink(Link{ID: "K1", Source: "o1", Target: "o2", Type: "t"})

	var buf bytes.Buffer
	tr := MultiTracer{&LogTracer{W: &buf}}
	action := Action{
		Name: "demo-action",
		Ops: []SubOp{
			{Kind: OpSetAttrs, Object: "o1", Attrs: Attrs{"v": 10}},
			{Kind: OpCreateLink, Link: Link{ID: "K2", Source: "o1", Target: "o2"}},
			{Kind: OpDeleteLink, Link: Link{ID: "K1"}},
			{Kind: OpValidate, Object: "o2"},
		},
		Inject: &FaultPlan{
			Apply: map[int]FaultPoint{3: FaultApplyFail}, // op3 business reject
			Undo:  map[int]FaultPoint{1: FaultUndoFail},  // undo of op1 fails
		},
	}
	t.Logf("INPUT action=%s ops=%d injectApply=%v injectUndo=%v",
		action.Name, len(action.Ops), action.Inject.Apply, action.Inject.Undo)

	r := NewEngine(g).WithTracer(tr).Execute(action)
	t.Logf("\nSTEP TRACE:\n%s", buf.String())
	t.Logf("FINAL verdict basis: commit=%v primary=%s failedOp=%d "+
		"compensationFailures=%d (reason priority: contaminated > business > contention > compensation)",
		r.Committed, r.Primary, r.FailedOpIndex, len(r.CompFailures))

	if r.Committed || r.FailedOpIndex != 3 || len(r.CompFailures) != 1 {
		t.Fatalf("unexpected verdict: %+v", r)
	}
	if !strings.Contains(buf.String(), "reg#") ||
		!strings.Contains(buf.String(), "undo") {
		t.Fatal("log missing registration/undo evidence")
	}
}
