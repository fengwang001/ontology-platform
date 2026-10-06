package defassign

import (
	"reflect"
	"testing"
)

func checkProg(t *testing.T, reg *Registry) []Diagnostic {
	t.Helper()
	diags, ierr := Check(reg)
	if ierr != nil {
		t.Fatalf("unexpected input error: %v", ierr)
	}
	return diags
}

func diagStrings(diags []Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.String()
	}
	return out
}

func expectDiags(t *testing.T, reg *Registry, want ...string) {
	t.Helper()
	got := diagStrings(checkProg(t, reg))
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("program:\n%s\nwant no diagnostics, got %v", reg.String(), got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("program:\n%s\ngot  %v\nwant %v", reg.String(), got, want)
	}
}

// Both branches assign: the merge keeps the variable assigned.
func TestBranchMergeBothAssigned(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a1 := reg.Assign("x") // #0
	a2 := reg.Assign("x") // #1
	ifN := reg.If(CondUnknown, []int{a1}, []int{a2})
	r := reg.Read("x")
	reg.SetRoot(reg.Block(ifN, r))
	expectDiags(t, reg)
}

// Only one branch assigns: the read is unassigned along the other branch.
func TestBranchMergeOneSide(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a1 := reg.Assign("x") // #0
	ifN := reg.If(CondUnknown, []int{a1}, nil)
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(ifN, r))
	expectDiags(t, reg, "#2 read(x): possibly unassigned on paths: if#1:else")
}

// A constantly-true condition prunes the else side: it neither joins nor
// produces diagnostics.
func TestConstTruePrunesElse(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	deadA := reg.Assign("x") // #0, unreachable
	deadR := reg.Read("x")   // #1, unreachable
	a := reg.Assign("x")     // #2
	ifN := reg.If(CondTrue, []int{a}, []int{deadA, deadR})
	r := reg.Read("x") // #4
	reg.SetRoot(reg.Block(ifN, r))
	expectDiags(t, reg)
}

// A constantly-false condition prunes the then side.
func TestConstFalsePrunesThen(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0, unreachable
	ifN := reg.If(CondFalse, []int{a}, nil)
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(ifN, r))
	expectDiags(t, reg, "#2 read(x): possibly unassigned on paths: if#1:else")
}

// A loop may execute zero times: body assignments do not count after it.
func TestLoopZeroTrips(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	loop := reg.Loop([]int{a}, false)
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(loop, r))
	expectDiags(t, reg, "#2 read(x): possibly unassigned on paths: loop#1:zero")
}

// A loop annotated at-least-once: the body definitely ran.
func TestLoopAtLeastOnce(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	loop := reg.Loop([]int{a}, true)
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(loop, r))
	expectDiags(t, reg)
}

// A read inside a loop may not use the previous iteration's assignment,
// regardless of the at-least-once annotation.
func TestLoopRejectsPreviousIteration(t *testing.T) {
	for _, atLeastOnce := range []bool{false, true} {
		reg := NewRegistry()
		reg.Declare("x")
		r := reg.Read("x")   // #0
		a := reg.Assign("x") // #1, live via the back edge: not dead
		loop := reg.Loop([]int{r, a}, atLeastOnce)
		reg.SetRoot(loop)
		expectDiags(t, reg, "#0 read(x): possibly unassigned on paths: loop#2:body")
	}
}

// A break path does not join the fall-through after the break, but it does
// join the loop exit.
func TestBreakSkipsFallThroughJoin(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	brk := reg.Break() // #0
	ifN := reg.If(CondUnknown, []int{brk}, nil)
	a := reg.Assign("x") // #2
	loop := reg.Loop([]int{ifN, a}, false)
	r := reg.Read("x") // #4
	reg.SetRoot(reg.Block(loop, r))
	expectDiags(t, reg,
		"#4 read(x): possibly unassigned on paths: loop#3:body -> if#1:then -> break#0 | loop#3:zero")
}

// The break path's own state reaches the loop exit.
func TestBreakStateReachesLoopExit(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	brk := reg.Break()   // #1
	loop := reg.Loop([]int{a, brk}, true)
	r := reg.Read("x") // #3
	reg.SetRoot(reg.Block(loop, r))
	expectDiags(t, reg)
}

// Statements after a break inside the body are unreachable.
func TestBreakMakesRestUnreachable(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	brk := reg.Break() // #0
	r := reg.Read("x") // #1, unreachable
	loop := reg.Loop([]int{brk, r}, false)
	reg.SetRoot(loop)
	expectDiags(t, reg)
}

// A handler only sees variables assigned before the protected body.
func TestHandlerSeesPreTryState(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0, never read anywhere: dead
	hr := reg.Read("x")  // #1
	h := reg.Handler(hr)
	try := reg.Try([]int{a}, []int{h}, nil)
	reg.SetRoot(try)
	expectDiags(t, reg,
		"#0 assign(x): never read",
		"#1 read(x): possibly unassigned on paths: try#3:handler-0")
}

// A variable assigned before the try is visible inside the handler.
func TestHandlerUsesPreTryAssignment(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	pre := reg.Assign("x") // #0, read by the handler: live
	a := reg.Assign("x")   // #1, invisible after the structure: dead
	hr := reg.Read("x")    // #2
	h := reg.Handler(hr)
	try := reg.Try([]int{a}, []int{h}, nil)
	reg.SetRoot(reg.Block(pre, try))
	expectDiags(t, reg, "#1 assign(x): never read")
}

// A variable assigned only in the cleanup region is assigned afterwards.
func TestCleanupAssignmentCounts(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	try := reg.Try(nil, nil, []int{a})
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(try, r))
	expectDiags(t, reg)
}

// A cleanup region ending in a jump makes the following code unreachable.
func TestCleanupJumpMakesRestUnreachable(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	ret := reg.Return() // #0
	try := reg.Try(nil, nil, []int{ret})
	r := reg.Read("x")   // #2, unreachable
	a := reg.Assign("x") // #3, unreachable
	reg.SetRoot(reg.Block(try, r, a))
	expectDiags(t, reg)
}

// An assignment at the very end of the protected body does not count after
// the structure: an unhandled throw skips it. Even with no handlers and an
// empty cleanup region it still does not count.
func TestBodyEndAssignmentSkippedByThrow(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	try := reg.Try([]int{a}, nil, nil)
	r := reg.Read("x") // #2
	reg.SetRoot(reg.Block(try, r))
	expectDiags(t, reg, "#2 read(x): possibly unassigned on paths: try#1:unhandled")
}

// The cleanup region joins body end, handler ends and the unhandled-throw
// state; only pre-try assignments are guaranteed there.
func TestCleanupEntryIsJoinOfAllSources(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	bodyA := reg.Assign("x") // #0
	ha := reg.Assign("x")    // #1
	h := reg.Handler(ha)
	cr := reg.Read("x") // #3, in cleanup
	try := reg.Try([]int{bodyA}, []int{h}, []int{cr})
	reg.SetRoot(try)
	expectDiags(t, reg, "#3 read(x): possibly unassigned on paths: try#4:unhandled")
}

// An assignment never read before being reassigned is dead.
func TestDeadAssignBasic(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a1 := reg.Assign("x") // #0
	a2 := reg.Assign("x") // #1
	r := reg.Read("x")    // #2
	reg.SetRoot(reg.Block(a1, a2, r))
	expectDiags(t, reg, "#0 assign(x): never read")
}

// An assignment inside a loop read at the top of the next iteration is not
// dead (back edge), even though the read may not rely on it for definite
// assignment.
func TestDeadAssignLoopBackEdge(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	pre := reg.Assign("x") // #0
	r := reg.Read("x")     // #1
	a := reg.Assign("x")   // #2
	loop := reg.Loop([]int{r, a}, false)
	reg.SetRoot(reg.Block(pre, loop))
	expectDiags(t, reg)
}

// An assignment inside a loop that is never read anywhere is dead.
func TestDeadAssignInsideLoop(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("x") // #0
	loop := reg.Loop([]int{a}, false)
	reg.SetRoot(loop)
	expectDiags(t, reg, "#0 assign(x): never read")
}

// Unassigned-read and dead-assign diagnostics are independent and never
// cancel each other.
func TestDiagnosticsDoNotCancel(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	reg.Declare("y")
	a := reg.Assign("x") // #0
	r := reg.Read("y")   // #1
	reg.SetRoot(reg.Block(a, r))
	expectDiags(t, reg,
		"#0 assign(x): never read",
		"#1 read(y): possibly unassigned on paths: <entry>")
}

// A break inside a try inside a loop is legal.
func TestBreakInsideTryInsideLoop(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	brk := reg.Break() // #0
	try := reg.Try([]int{brk}, nil, nil)
	loop := reg.Loop([]int{try}, false)
	reg.SetRoot(loop)
	expectDiags(t, reg)
}

func expectInputError(t *testing.T, reg *Registry, want ErrCategory) {
	t.Helper()
	diags, ierr := Check(reg)
	if ierr == nil {
		t.Fatalf("want input error category %v, got none", want)
	}
	if ierr.Category != want {
		t.Fatalf("want category %v, got %v (%v)", want, ierr.Category, ierr)
	}
	if diags != nil {
		t.Fatalf("invalid input must not produce diagnostics, got %v", diags)
	}
}

// Rejection order: structure cycle > undeclared variable > illegal break >
// orphan handler.
func TestErrorOrderCycleFirst(t *testing.T) {
	reg := NewRegistry() // x undeclared on purpose
	a := reg.Assign("x")
	inner := reg.Block(a)
	root := reg.Block(inner, inner) // shared child: not a tree
	reg.SetRoot(root)
	expectInputError(t, reg, ErrCycle)
}

func TestErrorOrderUndeclaredBeforeBreak(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	a := reg.Assign("y") // undeclared
	b := reg.Break()     // outside any loop
	reg.SetRoot(reg.Block(a, b))
	expectInputError(t, reg, ErrUndeclaredVar)
}

func TestErrorOrderBreakBeforeOrphanHandler(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	b := reg.Break() // outside any loop
	reg.Handler()    // orphan
	reg.SetRoot(reg.Block(b))
	expectInputError(t, reg, ErrBreakOutsideLoop)
}

func TestErrorOrphanHandler(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("x")
	reg.Handler() // orphan
	a := reg.Assign("x")
	reg.SetRoot(reg.Block(a))
	expectInputError(t, reg, ErrOrphanHandler)
}
