package bce

import (
	"fmt"
	"sync"
	"testing"
)

func blk(id string, instrs ...Instr) *Block { return &Block{ID: id, Instrs: instrs} }

func prog(entry string, blocks ...*Block) *Program { return &Program{Entry: entry, Blocks: blocks} }

func mustAnalyze(t *testing.T, p *Program) *Report {
	t.Helper()
	rep, err := Analyze(p)
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	return rep
}

func decision(t *testing.T, rep *Report, id string) Decision {
	t.Helper()
	for _, d := range rep.Decisions {
		if d.CheckID == id {
			return d
		}
	}
	t.Fatalf("no decision for check %s", id)
	return Decision{}
}

// Fact kinds 1+2: constant index and constant length suffice.
func TestConstIdxConstLenRemoved(t *testing.T) {
	p := prog("b0", blk("b0",
		NewArray{Dst: "a", Size: C(8)},
		Const{Dst: "i", Val: 3},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); !d.Removed {
		t.Fatalf("c1 should be removed:\n%s", rep)
	}
	if rep.Removed != 1 || rep.Kept != 0 {
		t.Fatalf("summary mismatch: removed=%d kept=%d", rep.Removed, rep.Kept)
	}
}

// Constant index alone: without a length fact the upper bound is unproven.
func TestConstIdxWithoutLenKept(t *testing.T) {
	p := prog("b0", blk("b0",
		Input{Dst: "n"},
		NewArray{Dst: "a", Size: V("n")},
		Const{Dst: "i", Val: 3},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if !d.Lower.Proven {
		t.Fatalf("lower bound should be proven:\n%s", rep)
	}
	if d.Upper.Proven || d.Upper.Cause != CauseOutOfScope {
		t.Fatalf("upper should be unproven/out-of-scope, got %+v", d.Upper)
	}
}

// Constant length alone: the index bound is unproven on both sides.
func TestConstLenWithoutIdxKept(t *testing.T) {
	p := prog("b0", blk("b0",
		Input{Dst: "i"},
		NewArray{Dst: "a", Size: C(8)},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d.Lower.Proven || d.Upper.Proven {
		t.Fatalf("both bounds should be unproven:\n%s", rep)
	}
	if d.Lower.Cause != CauseOutOfScope || d.Upper.Cause != CauseOutOfScope {
		t.Fatalf("causes should be out-of-scope: %+v %+v", d.Lower, d.Upper)
	}
}

// Fact kind 3: path comparison conditions suffice on the true side.
func TestPathConditionRemoved(t *testing.T) {
	p := prog("b0",
		blk("b0",
			Input{Dst: "i"},
			NewArray{Dst: "a", Size: C(8)},
			LenOf{Dst: "n", Arr: "a"},
			Br{A: V("i"), Op: LT, B: V("n"), Then: "b1", Else: "b3"},
		),
		blk("b1",
			Br{A: V("i"), Op: GE, B: C(0), Then: "b2", Else: "b3"},
		),
		blk("b2",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Ret{},
		),
		blk("b3", Ret{}),
	)
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); !d.Removed {
		t.Fatalf("c1 should be removed:\n%s", rep)
	}
}

// Path condition missing the lower side: kept with lower unproven.
func TestPathConditionLowerMissing(t *testing.T) {
	p := prog("b0",
		blk("b0",
			Input{Dst: "i"},
			NewArray{Dst: "a", Size: C(8)},
			LenOf{Dst: "n", Arr: "a"},
			Br{A: V("i"), Op: LT, B: V("n"), Then: "b1", Else: "b2"},
		),
		blk("b1",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Ret{},
		),
		blk("b2", Ret{}),
	)
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d.Lower.Proven || d.Lower.Cause != CauseOutOfScope {
		t.Fatalf("lower should be unproven/out-of-scope, got %+v", d.Lower)
	}
	if !d.Upper.Proven {
		t.Fatalf("upper should be proven by i<n:\n%s", rep)
	}
}

// Fact kind 4: a passed check makes later checks on the same pair redundant.
func TestPriorCheckRemoved(t *testing.T) {
	p := prog("b0", blk("b0",
		Input{Dst: "i"},
		NewArray{Dst: "a", Size: C(8)},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		Check{ID: "c2", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d := decision(t, rep, "c2"); !d.Removed {
		t.Fatalf("c2 should be removed via passed check:\n%s", rep)
	}
	if rep.Removed != 1 || rep.Kept != 1 {
		t.Fatalf("summary mismatch: %s", rep)
	}
}

// Facts disagree across a join: only facts holding on both sides survive.
func TestJoinLostFact(t *testing.T) {
	p := prog("b0",
		blk("b0",
			Input{Dst: "i"},
			NewArray{Dst: "a", Size: C(8)},
			Br{A: V("i"), Op: LT, B: C(8), Then: "b1", Else: "b2"},
		),
		blk("b1",
			Br{A: V("i"), Op: GE, B: C(0), Then: "b3", Else: "b2"},
		),
		blk("b2", Jmp{To: "b3"}),
		blk("b3",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Ret{},
		),
	)
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d.Lower.Cause != CauseJoinLost {
		t.Fatalf("lower cause should be join-lost-fact, got %v", d.Lower.Cause)
	}
	if d.Upper.Cause != CauseJoinLost {
		t.Fatalf("upper cause should be join-lost-fact, got %v", d.Upper.Cause)
	}
}

// Loop induction, while-shape: i starts at 0, increments by 1, and the
// guard i < 8 dominates the check. Both bounds provable.
func TestLoopWhileRemoved(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 0},
			Jmp{To: "head"},
		),
		blk("head",
			Br{A: V("i"), Op: LT, B: C(8), Then: "body", Else: "exit"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Bin{Dst: "i", Op: Add, A: V("i"), B: C(1)},
			Jmp{To: "head"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); !d.Removed {
		t.Fatalf("c1 should be removed:\n%s", rep)
	}
}

// Loop induction, do-while-shape: the check precedes the guard, so only
// the induction argument can prove the upper bound.
func TestLoopDoWhileRemoved(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 0},
			Jmp{To: "body"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Bin{Dst: "i", Op: Add, A: V("i"), B: C(1)},
			Br{A: V("i"), Op: LT, B: C(8), Then: "body", Else: "exit"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); !d.Removed {
		t.Fatalf("c1 should be removed:\n%s", rep)
	}
}

// Equality boundary, do-while with i <= 8 and len 8: the last iteration
// accesses index 8, out of bounds. The closed comparison must not prove
// the open bound.
func TestLoopDoWhileInclusiveBoundKept(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 0},
			Jmp{To: "body"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Bin{Dst: "i", Op: Add, A: V("i"), B: C(1)},
			Br{A: V("i"), Op: LE, B: C(8), Then: "body", Else: "exit"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept (i can reach 8):\n%s", rep)
	}
	if !d.Lower.Proven {
		t.Fatalf("lower should still be proven by induction:\n%s", rep)
	}
}

// Equality boundary, do-while with i <= 7 and len 8: closed comparison
// against a strictly smaller bound suffices.
func TestLoopDoWhileInclusiveBoundRemoved(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 0},
			Jmp{To: "body"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Bin{Dst: "i", Op: Add, A: V("i"), B: C(1)},
			Br{A: V("i"), Op: LE, B: C(7), Then: "body", Else: "exit"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); !d.Removed {
		t.Fatalf("c1 should be removed:\n%s", rep)
	}
}

// While-shape with i <= 8 and len 8: the path condition i <= 8 cannot
// prove i < 8.
func TestLoopWhileInclusiveBoundKept(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 0},
			Jmp{To: "head"},
		),
		blk("head",
			Br{A: V("i"), Op: LE, B: C(8), Then: "body", Else: "exit"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Bin{Dst: "i", Op: Add, A: V("i"), B: C(1)},
			Jmp{To: "head"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c1"); d.Removed {
		t.Fatalf("c1 must be kept (i can equal 8):\n%s", rep)
	}
}

// The loop assignment invalidates the constant fact about the index.
func TestLoopAssignmentKillsFact(t *testing.T) {
	p := prog("b0",
		blk("b0",
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "i", Val: 3},
			Jmp{To: "head"},
		),
		blk("head",
			Br{A: V("i"), Op: LT, B: C(8), Then: "body", Else: "exit"},
		),
		blk("body",
			Check{ID: "c1", Arr: "a", Idx: "i"},
			Input{Dst: "i"},
			Jmp{To: "head"},
		),
		blk("exit", Ret{}),
	)
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d.Lower.Cause != CauseLoopKilled {
		t.Fatalf("lower cause should be loop-killed-fact, got %v", d.Lower.Cause)
	}
}

// Reassigning the array variable invalidates its length fact.
func TestArrayReassignKillsFacts(t *testing.T) {
	p := prog("b0", blk("b0",
		Input{Dst: "n"},
		NewArray{Dst: "a", Size: C(8)},
		NewArray{Dst: "b", Size: V("n")},
		Const{Dst: "i", Val: 3},
		ArrCopy{Dst: "a", Src: "b"},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	d := decision(t, rep, "c1")
	if d.Removed {
		t.Fatalf("c1 must be kept:\n%s", rep)
	}
	if d.Upper.Cause != CauseArrReassigned {
		t.Fatalf("upper cause should be array-reassigned, got %v", d.Upper.Cause)
	}
}

// A passed check is invalidated when the array is reassigned afterwards.
func TestPassedCheckInvalidatedByReassign(t *testing.T) {
	p := prog("b0", blk("b0",
		Input{Dst: "i"},
		NewArray{Dst: "a", Size: C(8)},
		NewArray{Dst: "b", Size: C(4)},
		Check{ID: "c1", Arr: "a", Idx: "i"},
		ArrCopy{Dst: "a", Src: "b"},
		Check{ID: "c2", Arr: "a", Idx: "i"},
		Ret{},
	))
	rep := mustAnalyze(t, p)
	if d := decision(t, rep, "c2"); d.Removed {
		t.Fatalf("c2 must be kept (a was reassigned):\n%s", rep)
	}
}

// Error rejection order: undefined references beat everything else.
func TestErrorOrderUndefinedFirst(t *testing.T) {
	p := prog("b0",
		blk("b0",
			Check{ID: "c1", Arr: "a", Idx: "ghost"}, // undefined scalar
			Ret{},
			Ret{}, // also multiple terminators
		),
		blk("b1", Ret{}), // also unreachable
	)
	p.Blocks[0].Instrs = append(p.Blocks[0].Instrs, NewArray{Dst: "a", Size: C(4)})
	_, err := Analyze(p)
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrUndefinedRef {
		t.Fatalf("want undefined-reference, got %v", err)
	}
}

// Multiple terminators beat unreachable blocks.
func TestErrorOrderMultiTermBeforeUnreachable(t *testing.T) {
	p := prog("b0",
		blk("b0", Ret{}, Jmp{To: "b0"}),
		blk("b1", Ret{}),
	)
	_, err := Analyze(p)
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrMultiTerminator {
		t.Fatalf("want multiple-terminators, got %v", err)
	}
}

// Unreachable blocks beat loop-entry errors.
func TestErrorOrderUnreachableBeforeLoop(t *testing.T) {
	p := prog("b0",
		blk("b0", Br{A: C(1), Op: LT, B: C(2), Then: "b1", Else: "b2"}),
		blk("b1", Jmp{To: "b3"}),
		blk("b2", Jmp{To: "b3"}),
		blk("b3", Br{A: C(1), Op: LT, B: C(2), Then: "b1", Else: "b4"}),
		blk("b4", Ret{}),
		blk("b5", Ret{}), // unreachable
	)
	_, err := Analyze(p)
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrUnreachableBlock {
		t.Fatalf("want unreachable-block, got %v", err)
	}
}

// A loop reachable from two sides without a dominating header is rejected.
func TestErrorLoopMultiEntry(t *testing.T) {
	p := prog("b0",
		blk("b0", Br{A: C(1), Op: LT, B: C(2), Then: "b1", Else: "b2"}),
		blk("b1", Jmp{To: "b3"}),
		blk("b2", Jmp{To: "b3"}),
		blk("b3", Br{A: C(1), Op: LT, B: C(2), Then: "b1", Else: "b4"}),
		blk("b4", Ret{}),
	)
	_, err := Analyze(p)
	e, ok := err.(*Error)
	if !ok || e.Kind != ErrLoopMultiEntry {
		t.Fatalf("want loop-multiple-entry, got %v", err)
	}
}

// Invalid input produces no partial result.
func TestErrorNoPartialResult(t *testing.T) {
	p := prog("b0", blk("b0", Check{ID: "c1", Arr: "nope", Idx: "nope"}, Ret{}))
	rep, err := Analyze(p)
	if err == nil || rep != nil {
		t.Fatalf("expected nil report on error, got %v %v", rep, err)
	}
}

// sampleProgram exercises all four fact kinds plus a kept check.
func sampleProgram() *Program {
	return prog("b0",
		blk("b0",
			Input{Dst: "i"},
			NewArray{Dst: "a", Size: C(8)},
			Const{Dst: "j", Val: 2},
			Check{ID: "k1", Arr: "a", Idx: "j"},
			Check{ID: "k2", Arr: "a", Idx: "i"},
			Check{ID: "k3", Arr: "a", Idx: "i"},
			Const{Dst: "w", Val: 0},
			Jmp{To: "head"},
		),
		blk("head",
			Br{A: V("w"), Op: LT, B: C(8), Then: "body", Else: "exit"},
		),
		blk("body",
			Check{ID: "k4", Arr: "a", Idx: "w"},
			Bin{Dst: "w", Op: Add, A: V("w"), B: C(1)},
			Jmp{To: "head"},
		),
		blk("exit", Ret{}),
	)
}

// Repeated analysis of the same input renders byte-identical reports.
func TestDeterministicReport(t *testing.T) {
	first := ""
	for k := 0; k < 20; k++ {
		rep := mustAnalyze(t, sampleProgram())
		s := rep.String()
		if k == 0 {
			first = s
			continue
		}
		if s != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", k, s, first)
		}
	}
}

// Independent inputs analyzed concurrently do not interfere.
func TestConcurrentAnalysis(t *testing.T) {
	want := map[string]string{}
	var programs []*Program
	for k := 0; k < 16; k++ {
		var p *Program
		if k%2 == 0 {
			p = sampleProgram()
		} else {
			p = prog("b0", blk("b0",
				NewArray{Dst: "a", Size: C(int64(k))},
				Const{Dst: "i", Val: int64(k % 3)},
				Check{ID: fmt.Sprintf("c%d", k), Arr: "a", Idx: "i"},
				Ret{},
			))
		}
		programs = append(programs, p)
		rep := mustAnalyze(t, p)
		want[fmt.Sprint(k)] = rep.String()
	}
	var wg sync.WaitGroup
	for k := range programs {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for iter := 0; iter < 5; iter++ {
				rep, err := Analyze(programs[k])
				if err != nil {
					t.Error(err)
					return
				}
				if rep.String() != want[fmt.Sprint(k)] {
					t.Errorf("program %d: concurrent result differs", k)
				}
			}
		}(k)
	}
	wg.Wait()
}

// Meet cost at joins does not depend on the number of unrelated variables:
// adding hundreds of unrelated variables (same CFG) leaves MeetOps and
// ProveOps unchanged, because only relevant facts are ever stored.
func TestMeetCostIndependentOfUnrelatedVars(t *testing.T) {
	var s1 Stats
	if _, err := AnalyzeWithStats(sampleProgram(), &s1); err != nil {
		t.Fatal(err)
	}

	grown := sampleProgram()
	for bi, b := range grown.Blocks {
		var extra []Instr
		for v := 0; v < 100; v++ {
			name := fmt.Sprintf("u%d_%d", bi, v)
			extra = append(extra,
				Const{Dst: name, Val: int64(v)},
				Bin{Dst: name, Op: Add, A: V(name), B: C(1)},
			)
		}
		b.Instrs = append(extra, b.Instrs...)
	}
	var s2 Stats
	rep2, err := AnalyzeWithStats(grown, &s2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.MeetOps != s2.MeetOps {
		t.Fatalf("MeetOps grew with unrelated variables: %d -> %d", s1.MeetOps, s2.MeetOps)
	}
	if s1.ProveOps != s2.ProveOps {
		t.Fatalf("ProveOps grew with unrelated variables: %d -> %d", s1.ProveOps, s2.ProveOps)
	}
	if rep2.Removed != 3 || rep2.Kept != 1 {
		t.Fatalf("unexpected decisions on grown program:\n%s", rep2)
	}
}

// The decision cost of a single check does not depend on the length of the
// program: adding many unrelated blocks leaves ProveOps unchanged.
func TestDecisionCostIndependentOfProgramLength(t *testing.T) {
	var s1 Stats
	if _, err := AnalyzeWithStats(sampleProgram(), &s1); err != nil {
		t.Fatal(err)
	}

	grown := sampleProgram()
	var side []*Block
	prev := "exit"
	for k := 0; k < 60; k++ {
		id := fmt.Sprintf("side%d", k)
		instrs := []Instr{
			Input{Dst: fmt.Sprintf("s%d", k)},
			Bin{Dst: fmt.Sprintf("s%d", k), Op: Sub, A: V(fmt.Sprintf("s%d", k)), B: C(1)},
			Jmp{To: prev},
		}
		side = append([]*Block{blk(id, instrs...)}, side...)
		prev = id
	}
	// Route head's else edge through the unrelated side chain (which ends
	// at exit) so every side block is reachable but irrelevant.
	for _, b := range grown.Blocks {
		if b.ID == "head" {
			br := b.Term().(Br)
			br.Else = prev
			b.Instrs[len(b.Instrs)-1] = br
		}
	}
	grown.Blocks = append(grown.Blocks, side...)

	var s2 Stats
	rep2, err := AnalyzeWithStats(grown, &s2)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ProveOps != s2.ProveOps {
		t.Fatalf("ProveOps grew with program length: %d -> %d", s1.ProveOps, s2.ProveOps)
	}
	if rep2.Removed != 3 || rep2.Kept != 1 {
		t.Fatalf("unexpected decisions on grown program:\n%s", rep2)
	}
}
