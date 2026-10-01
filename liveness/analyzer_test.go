package liveness

import (
	"errors"
	"testing"
)

func uins(uses ...string) Instruction {
	return Instruction{Uses: uses}
}

func dins(defs ...string) Instruction {
	return Instruction{Defs: defs}
}

func udins(uses []string, defs ...string) Instruction {
	return Instruction{Uses: uses, Defs: defs}
}

func logSpecs(t *testing.T, specs []BlockSpec) {
	t.Helper()
	for _, s := range specs {
		t.Logf("input block %d: successors=%v instructions=%v", s.ID, s.Successors, s.Instructions)
	}
}

func buildSealed(t *testing.T, specs []BlockSpec) *Analyzer {
	t.Helper()
	a := NewAnalyzer()
	for _, s := range specs {
		if err := a.AddBlock(s); err != nil {
			t.Fatalf("AddBlock(%d) unexpected error: %v", s.ID, err)
		}
	}
	if err := a.Seal(); err != nil {
		t.Fatalf("Seal unexpected error: %v", err)
	}
	return a
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertAgainstNaive(t *testing.T, a *Analyzer, specs []BlockSpec) {
	t.Helper()
	want := naiveSolve(specs)
	got, err := a.Results()
	if err != nil {
		t.Fatalf("Results unexpected error: %v", err)
	}
	for _, r := range got {
		w := want[r.ID]
		t.Logf("block %d: UE=%v Def=%v LiveIn=%v LiveOut=%v | naive LiveIn=%v LiveOut=%v",
			r.ID, r.UpwardExposed, r.Defined, r.LiveIn, r.LiveOut, w[0], w[1])
		if !equalStrings(r.LiveIn, w[0]) || !equalStrings(r.LiveOut, w[1]) {
			t.Errorf("block %d mismatch: got in=%v out=%v, want in=%v out=%v",
				r.ID, r.LiveIn, r.LiveOut, w[0], w[1])
		}
	}
}

// A single instruction "x = x + 1" uses x before defining it, so x is UE.
func TestUseBeforeDefInSameInstruction(t *testing.T) {
	specs := []BlockSpec{{
		ID:           0,
		Instructions: []Instruction{udins([]string{"x"}, "x")},
	}}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	ue, err := a.UpwardExposed(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judgement: use-before-def inside one instruction => UE=%v", ue)
	if !equalStrings(ue, []string{"x"}) {
		t.Fatalf("UE = %v, want [x]", ue)
	}
	in, _ := a.LiveIn(0)
	t.Logf("output: LiveIn=%v (x may be read while undefined)", in)
	if !equalStrings(in, []string{"x"}) {
		t.Fatalf("LiveIn = %v, want [x]", in)
	}
	assertAgainstNaive(t, a, specs)
}

// A variable defined first and only used later inside the same block is not
// upward exposed and need not be live on entry.
func TestDefBeforeUseNotUpwardExposed(t *testing.T) {
	specs := []BlockSpec{{
		ID: 0,
		Instructions: []Instruction{
			dins("y"),
			uins("y"),
		},
	}}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	ue, _ := a.UpwardExposed(0)
	in, _ := a.LiveIn(0)
	t.Logf("judgement: y is defined before use => UE=%v LiveIn=%v", ue, in)
	if len(ue) != 0 || len(in) != 0 {
		t.Fatalf("expected empty UE and LiveIn, got UE=%v LiveIn=%v", ue, in)
	}
	assertAgainstNaive(t, a, specs)
}

// LiveOut is the union of successors' LiveIn; the entry live-in is the set
// of variables possibly used while undefined.
func TestMultipleSuccessorsUnion(t *testing.T) {
	specs := []BlockSpec{
		{ID: 0, Instructions: []Instruction{{}}, Successors: []int{1, 2}},
		{ID: 1, Instructions: []Instruction{udins([]string{"a"}, "t")}},
		{ID: 2, Instructions: []Instruction{udins([]string{"b"}, "u")}},
	}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	out, _ := a.LiveOut(0)
	entry, _ := a.EntryLiveIn()
	t.Logf("judgement: LiveOut(0)=LiveIn(1) union LiveIn(2)=%v; entry undefined-use set=%v", out, entry)
	if !equalStrings(out, []string{"a", "b"}) {
		t.Fatalf("LiveOut(0) = %v, want [a b]", out)
	}
	if !equalStrings(entry, []string{"a", "b"}) {
		t.Fatalf("EntryLiveIn = %v, want [a b]", entry)
	}
	assertAgainstNaive(t, a, specs)
}

// A loop forces fixpoint iteration; variables carried around the back edge
// become live and iteration from the empty set converges to the least fixpoint.
func TestLoopConvergence(t *testing.T) {
	specs := []BlockSpec{
		{ID: 0, Instructions: []Instruction{udins([]string{"i"}, "i")}, Successors: []int{1}},
		{ID: 1, Instructions: []Instruction{uins("n")}, Successors: []int{2}},
		{ID: 2, Instructions: []Instruction{uins("i", "n"), dins("i")}, Successors: []int{1, 3}},
		{ID: 3, Instructions: []Instruction{uins("i")}},
	}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	assertAgainstNaive(t, a, specs)
	in2, _ := a.LiveIn(2)
	t.Logf("judgement: back edge 2->1 makes i,n propagate around the loop; LiveIn(2)=%v", in2)
	if !equalStrings(in2, []string{"i", "n"}) {
		t.Fatalf("LiveIn(2) = %v, want [i n]", in2)
	}
	entry, _ := a.EntryLiveIn()
	if !equalStrings(entry, []string{"i", "n"}) {
		t.Fatalf("EntryLiveIn = %v, want [i n]", entry)
	}
}

// A variable defined on only one branch is still live at the merge point and
// at the block that branches into both paths.
func TestDefinedOnOneBranchStillLiveAtMerge(t *testing.T) {
	specs := []BlockSpec{
		{ID: 0, Instructions: []Instruction{{}}, Successors: []int{1, 2}},
		{ID: 1, Instructions: []Instruction{dins("x")}, Successors: []int{3}},
		{ID: 2, Instructions: []Instruction{{}}, Successors: []int{3}},
		{ID: 3, Instructions: []Instruction{uins("x")}},
	}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	out2, _ := a.LiveOut(2)
	out0, _ := a.LiveOut(0)
	t.Logf("judgement: x defined only on branch 1 but read at merge 3 => LiveOut(2)=%v LiveOut(0)=%v", out2, out0)
	if !equalStrings(out2, []string{"x"}) {
		t.Fatalf("LiveOut(2) = %v, want [x]", out2)
	}
	if !equalStrings(out0, []string{"x"}) {
		t.Fatalf("LiveOut(0) = %v, want [x]", out0)
	}
	assertAgainstNaive(t, a, specs)
}

// Entry block reading a variable nobody defines reports it via EntryLiveIn.
func TestEntryUsesUndefinedVariable(t *testing.T) {
	specs := []BlockSpec{
		{ID: 0, Instructions: []Instruction{uins("undefinedVar"), dins("out")}},
	}
	logSpecs(t, specs)
	a := buildSealed(t, specs)
	entry, err := a.EntryLiveIn()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judgement: entry reads undefinedVar and no block defines it => EntryLiveIn=%v", entry)
	if !equalStrings(entry, []string{"undefinedVar"}) {
		t.Fatalf("EntryLiveIn = %v, want [undefinedVar]", entry)
	}
	assertAgainstNaive(t, a, specs)
}

// Successors may reference blocks that do not exist yet; adding the target
// later makes sealing succeed with the same results as the naive solver.
func TestForwardReferenceFilledLater(t *testing.T) {
	a := NewAnalyzer()
	if err := a.AddBlock(BlockSpec{ID: 0, Successors: []int{1}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: block 0 added with forward reference to block 1 (not yet present)")
	sealErr := a.Seal()
	if !errors.Is(sealErr, ErrMissingSucc) {
		t.Fatalf("first Seal = %v, want ErrMissingSucc", sealErr)
	}
	ms, ok := sealErr.(*MissingSuccessorError)
	if !ok || ms.BlockID != 0 || ms.Successor != 1 {
		t.Fatalf("error detail = %#v, want block 0 successor 1", sealErr)
	}
	t.Logf("judgement: seal before filling forward ref rejected (%v); data unchanged, can continue", sealErr)

	if err := a.AddBlock(BlockSpec{ID: 1, Instructions: []Instruction{uins("fwd")}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Seal(); err != nil {
		t.Fatalf("second Seal = %v, want nil", err)
	}
	specs := []BlockSpec{
		{ID: 0, Successors: []int{1}},
		{ID: 1, Instructions: []Instruction{uins("fwd")}},
	}
	assertAgainstNaive(t, a, specs)
	out0, _ := a.LiveOut(0)
	t.Logf("output after filling reference: LiveOut(0)=%v", out0)
	if !equalStrings(out0, []string{"fwd"}) {
		t.Fatalf("LiveOut(0) = %v, want [fwd]", out0)
	}
}
