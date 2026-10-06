package bce

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func blk(name string, instrs []Instr, terms ...Term) *Block {
	return &Block{Name: name, Instrs: instrs, Terms: terms}
}

func mkProg(name string, vars, inputs, arrays []string, lens map[string]int, blocks ...*Block) *Program {
	return &Program{Name: name, Vars: vars, Inputs: inputs, Arrays: arrays, Lengths: lens, Entry: blocks[0].Name, Blocks: blocks}
}

func analyzeOrFail(t *testing.T, p *Program) *Report {
	t.Helper()
	r, err := Analyze(p)
	if err != nil {
		t.Fatalf("Analyze(%s) failed: %v", p.Name, err)
	}
	return r
}

func decisionOf(t *testing.T, r *Report, id int) Decision {
	t.Helper()
	for _, d := range r.Decisions {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no decision for check #%d", id)
	return Decision{}
}

func hasReason(d Decision, reason string) bool {
	for _, r := range d.Reasons {
		if r == reason {
			return true
		}
	}
	return false
}

func mustAnalyze(t *testing.T, p *Program) *Report {
	t.Helper()
	return analyzeOrFail(t, p)
}

// --- Four fact classes, each sufficient and each necessary ---

func TestConstIndexAndConstLen(t *testing.T) {
	p := mkProg("const", nil, nil, []string{"a", "c"}, map[string]int{"a": 5},
		blk("B0", []Instr{
			Check("a", ConstOp(3)),  // #0: 0<=3<5 provable -> REMOVE
			Check("a", ConstOp(5)),  // #1: upper unprovable -> KEEP
			Check("a", ConstOp(-1)), // #2: lower unprovable -> KEEP
			Check("c", ConstOp(3)),  // #3: no length fact for c -> KEEP upper
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); !d.Removed {
		t.Fatalf("check #0 should be removed, reasons=%v", d.Reasons)
	}
	if d := decisionOf(t, r, 1); d.Removed || !hasReason(d, ReasonUpper) {
		t.Fatalf("check #1 should be kept with %q, got %v", ReasonUpper, d)
	}
	if d := decisionOf(t, r, 2); d.Removed || !hasReason(d, ReasonLower) {
		t.Fatalf("check #2 should be kept with %q, got %v", ReasonLower, d)
	}
	if d := decisionOf(t, r, 3); d.Removed || !hasReason(d, ReasonUpper) {
		t.Fatalf("check #3 should be kept with %q, got %v", ReasonUpper, d)
	}
}

func TestBranchConditionFacts(t *testing.T) {
	p := mkProg("branch", []string{"i"}, []string{"i"}, []string{"a"}, map[string]int{"a": 5},
		blk("B0", nil, Branch(VarOp("i"), "<", ConstOp(5), "B1", "B3")),
		blk("B1", nil, Branch(VarOp("i"), ">=", ConstOp(0), "B2", "B3")),
		blk("B2", []Instr{Check("a", VarOp("i"))}, Jump("B3")),
		blk("B3", nil, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); !d.Removed {
		t.Fatalf("check #0 should be removed via path conditions, reasons=%v", d.Reasons)
	}
}

func TestUpperBoundOnlyKeepsCheck(t *testing.T) {
	p := mkProg("upperonly", []string{"i"}, []string{"i"}, []string{"a"}, map[string]int{"a": 5},
		blk("B0", nil, Branch(VarOp("i"), "<", ConstOp(5), "B1", "B2")),
		blk("B1", []Instr{Check("a", VarOp("i"))}, Jump("B2")),
		blk("B2", nil, Return()),
	)
	r := mustAnalyze(t, p)
	d := decisionOf(t, r, 0)
	if d.Removed {
		t.Fatalf("check must be kept: only upper bound proven")
	}
	if !hasReason(d, ReasonLower) || hasReason(d, ReasonUpper) {
		t.Fatalf("reasons should be exactly [%s], got %v", ReasonLower, d.Reasons)
	}
}

func TestPassedCheckFact(t *testing.T) {
	p := mkProg("passed", []string{"i"}, []string{"i"}, []string{"a"}, map[string]int{"a": 5},
		blk("B0", []Instr{
			Check("a", VarOp("i")), // #0: nothing known -> KEEP
			Check("a", VarOp("i")), // #1: passed #0 -> REMOVE
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); d.Removed {
		t.Fatalf("check #0 must be kept")
	}
	if d := decisionOf(t, r, 1); !d.Removed {
		t.Fatalf("check #1 should be removed via passed check #0")
	}
}

func TestVariableReassignmentKillsFacts(t *testing.T) {
	p := mkProg("reassign", []string{"i", "j"}, []string{"j"}, []string{"a"}, map[string]int{"a": 5},
		blk("B0", []Instr{
			AssignConst("i", 0),
			Check("a", VarOp("i")), // #0: i==0 -> REMOVE
			Assign("i", "j"),       // i touched: constant fact must die
			Check("a", VarOp("i")), // #1: KEEP
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); !d.Removed {
		t.Fatalf("check #0 should be removed")
	}
	if d := decisionOf(t, r, 1); d.Removed {
		t.Fatalf("check #1 must be kept: i was reassigned between reads")
	}
}

func TestUntouchedVariableKeepsValue(t *testing.T) {
	p := mkProg("untouched", []string{"i", "j"}, nil, []string{"a"}, map[string]int{"a": 5},
		blk("B0", []Instr{
			AssignConst("i", 0),
			Assign("j", "i"),
			AssignConst("i", 1),    // touches i, not j
			Check("a", VarOp("j")), // #0: j still 0 -> REMOVE
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); !d.Removed {
		t.Fatalf("check #0 should be removed: j was never reassigned")
	}
}

// --- Joins, loops, array reassignment ---

func TestJoinLosesFact(t *testing.T) {
	p := mkProg("join", []string{"x", "i"}, []string{"x"}, []string{"a"}, map[string]int{"a": 5},
		blk("B0", nil, Branch(VarOp("x"), ">", ConstOp(0), "B1", "B2")),
		blk("B1", []Instr{AssignConst("i", 0)}, Jump("B3")),
		blk("B2", nil, Jump("B3")),
		blk("B3", []Instr{Check("a", VarOp("i"))}, Return()),
	)
	r := mustAnalyze(t, p)
	d := decisionOf(t, r, 0)
	if d.Removed {
		t.Fatalf("check must be kept: i is constant on only one side of the join")
	}
	if !hasReason(d, ReasonJoinLoss) {
		t.Fatalf("expected reason %q, got %v", ReasonJoinLoss, d.Reasons)
	}
}

// loopProg builds: i = start; while i <op> bound { body...; i = i + delta }.
func loopProg(name string, start, bound int, op string, delta, arrLen int) *Program {
	return mkProg(name, []string{"i"}, nil, []string{"a"}, map[string]int{"a": arrLen},
		blk("B0", []Instr{AssignConst("i", start)}, Jump("H")),
		blk("H", nil, Branch(VarOp("i"), op, ConstOp(bound), "BD", "EX")),
		blk("BD", []Instr{Check("a", VarOp("i")), AssignAdd("i", "i", delta)}, Jump("H")),
		blk("EX", nil, Return()),
	)
}

func TestLoopInduction(t *testing.T) {
	cases := []struct {
		name          string
		start, bound  int
		op            string
		delta, arrLen int
		wantRemoved   bool
		wantReason    string
	}{
		{"strict-less", 0, 10, "<", 1, 10, true, ""},
		{"closed-bound-exact", 0, 9, "<=", 1, 10, true, ""},
		{"closed-bound-overflow", 0, 10, "<=", 1, 10, false, ReasonUpper},
		{"negative-start", -5, 10, "<", 1, 10, false, ReasonLower},
		{"decreasing", 9, 0, ">=", -1, 10, true, ""},
		{"decreasing-open", 9, -1, ">", -1, 10, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustAnalyze(t, loopProg(tc.name, tc.start, tc.bound, tc.op, tc.delta, tc.arrLen))
			d := decisionOf(t, r, 0)
			if d.Removed != tc.wantRemoved {
				t.Fatalf("removed=%v, want %v (reasons=%v)", d.Removed, tc.wantRemoved, d.Reasons)
			}
			if tc.wantReason != "" && !hasReason(d, tc.wantReason) {
				t.Fatalf("expected reason %q, got %v", tc.wantReason, d.Reasons)
			}
		})
	}
}

func TestLoopAssignmentInvalidatesFact(t *testing.T) {
	p := mkProg("loopkill", []string{"i"}, nil, []string{"a"}, map[string]int{"a": 5},
		blk("B0", []Instr{AssignConst("i", 0)}, Jump("H")),
		blk("H", nil, Branch(VarOp("i"), "<", ConstOp(3), "BD", "EX")),
		blk("BD", []Instr{AssignAdd("i", "i", 1)}, Jump("H")),
		blk("EX", []Instr{Check("a", VarOp("i"))}, Return()),
	)
	r := mustAnalyze(t, p)
	d := decisionOf(t, r, 0)
	if d.Removed {
		t.Fatalf("check must be kept: the constant was killed by the loop assignment")
	}
	if !hasReason(d, ReasonLoopAssign) {
		t.Fatalf("expected reason %q, got %v", ReasonLoopAssign, d.Reasons)
	}
}

func TestArrayReassignmentInvalidates(t *testing.T) {
	p := mkProg("arrre", []string{"i"}, []string{"i"}, []string{"a", "b"},
		map[string]int{"a": 5, "b": 7},
		blk("B0", []Instr{
			Check("a", VarOp("i")), // #0: KEEP
			NewArray("a", 5),       // reassign: kills passed-check fact
			Check("a", VarOp("i")), // #1: KEEP with array-reassigned reason
			AssignArray("a", "b"),  // copy assignment also invalidates
			Check("a", VarOp("i")), // #2: KEEP with array-reassigned reason
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); d.Removed {
		t.Fatalf("check #0 must be kept")
	}
	for _, id := range []int{1, 2} {
		d := decisionOf(t, r, id)
		if d.Removed {
			t.Fatalf("check #%d must be kept after array reassignment", id)
		}
		if !hasReason(d, ReasonArrayReassigned) {
			t.Fatalf("check #%d: expected reason %q, got %v", id, ReasonArrayReassigned, d.Reasons)
		}
	}
}

func TestNewArrayShrinksLength(t *testing.T) {
	p := mkProg("shrink", nil, nil, []string{"a"}, map[string]int{"a": 5},
		blk("B0", []Instr{
			Check("a", ConstOp(3)), // #0: REMOVE (len 5)
			NewArray("a", 3),       // now len(a)==3
			Check("a", ConstOp(3)), // #1: 3 < 3 false -> KEEP upper
		}, Return()),
	)
	r := mustAnalyze(t, p)
	if d := decisionOf(t, r, 0); !d.Removed {
		t.Fatalf("check #0 should be removed")
	}
	if d := decisionOf(t, r, 1); d.Removed || !hasReason(d, ReasonUpper) {
		t.Fatalf("check #1 must be kept with %q, got %v", ReasonUpper, d)
	}
}

// --- Input errors and their rejection order ---

func errProg(mutate func(*Program)) *Program {
	p := mkProg("err", []string{"x"}, nil, []string{"a"}, map[string]int{"a": 3},
		blk("B0", nil, Branch(VarOp("x"), ">", ConstOp(0), "H", "L"), Jump("H")), // multi terminator
		blk("H", []Instr{Check("a", VarOp("ghost"))}, Jump("L")),                 // undefined ref
		blk("L", nil, Jump("H")),
		blk("U", nil, Return()), // unreachable; {H,L} has two entries
	)
	if mutate != nil {
		mutate(p)
	}
	return p
}

func TestErrorOrder(t *testing.T) {
	// All four defects present: undefined reference wins.
	if _, err := Analyze(errProg(nil)); err == nil || err.(*Error).Kind != ErrUndefinedRef {
		t.Fatalf("want ErrUndefinedRef, got %v", err)
	}
	// Fix the reference: multiple terminators wins.
	p1 := errProg(func(p *Program) { p.Block("H").Instrs[0] = Check("a", VarOp("x")) })
	if _, err := Analyze(p1); err == nil || err.(*Error).Kind != ErrMultiTerm {
		t.Fatalf("want ErrMultiTerm, got %v", err)
	}
	// Fix the terminator: unreachable block wins.
	p2 := errProg(func(p *Program) {
		p.Block("H").Instrs[0] = Check("a", VarOp("x"))
		p.Block("B0").Terms = []Term{Branch(VarOp("x"), ">", ConstOp(0), "H", "L")}
	})
	if _, err := Analyze(p2); err == nil || err.(*Error).Kind != ErrUnreachable {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
	// Drop the unreachable block: the multi-entry loop wins.
	p3 := errProg(func(p *Program) {
		p.Block("H").Instrs[0] = Check("a", VarOp("x"))
		p.Block("B0").Terms = []Term{Branch(VarOp("x"), ">", ConstOp(0), "H", "L")}
		p.Blocks = p.Blocks[:3]
	})
	if _, err := Analyze(p3); err == nil || err.(*Error).Kind != ErrLoopMultiEntry {
		t.Fatalf("want ErrLoopMultiEntry, got %v", err)
	}
	// Single-entry loop: accepted, no partial results from earlier errors.
	p4 := mkProg("ok", []string{"x"}, nil, []string{"a"}, map[string]int{"a": 3},
		blk("B0", nil, Jump("H")),
		blk("H", nil, Branch(VarOp("x"), ">", ConstOp(0), "L", "E")),
		blk("L", nil, Jump("H")),
		blk("E", nil, Return()),
	)
	if _, err := Analyze(p4); err != nil {
		t.Fatalf("valid program rejected: %v", err)
	}
}

// --- Determinism and concurrency ---

func TestRepeatAnalysisByteIdentical(t *testing.T) {
	p := loopProg("det", 0, 10, "<", 1, 10)
	first := mustAnalyze(t, p).String()
	for i := 0; i < 5; i++ {
		if got := mustAnalyze(t, p).String(); got != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, got, first)
		}
	}
}

func TestConcurrentIndependentInputs(t *testing.T) {
	const n = 24
	progs := make([]*Program, n)
	want := make([]string, n)
	for i := 0; i < n; i++ {
		r := rand.New(rand.NewSource(int64(1000 + i)))
		progs[i] = genProgram(r, i)
		want[i] = mustAnalyze(t, progs[i]).String()
	}
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := Analyze(progs[i])
			if err != nil {
				errs <- err.Error()
				return
			}
			if r.String() != want[i] {
				errs <- fmt.Sprintf("program %d: concurrent result differs", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// --- Verifiable complexity claims ---

func TestDecisionCostIndependentOfProgramSize(t *testing.T) {
	build := func(pad int) *Program {
		vars := []string{"i"}
		var instrs []Instr
		for n := 0; n < pad; n++ {
			v := fmt.Sprintf("v%d", n)
			vars = append(vars, v)
			instrs = append(instrs, AssignConst(v, n))
		}
		return mkProg(fmt.Sprintf("pad%d", pad), vars, []string{"i"}, []string{"a"},
			map[string]int{"a": 5},
			blk("B0", instrs, Jump("B1")),
			blk("B1", nil, Branch(VarOp("i"), "<", ConstOp(5), "B2", "B3")),
			blk("B2", nil, Branch(VarOp("i"), ">=", ConstOp(0), "B4", "B3")),
			blk("B4", []Instr{Check("a", VarOp("i"))}, Jump("B3")),
			blk("B3", nil, Return()),
		)
	}
	small := mustAnalyze(t, build(0))
	large := mustAnalyze(t, build(400))
	if small.Stats.FactsScanned != large.Stats.FactsScanned {
		t.Fatalf("proof cost grew with program size: %d vs %d",
			small.Stats.FactsScanned, large.Stats.FactsScanned)
	}
	if !decisionOf(t, large, 0).Removed {
		t.Fatalf("check should be removed in both programs")
	}
}

func TestMeetCostIndependentOfUnrelatedVariables(t *testing.T) {
	build := func(extra int) *Program {
		vars := []string{"x"}
		for n := 0; n < extra; n++ {
			vars = append(vars, fmt.Sprintf("u%d", n)) // declared, never used
		}
		return mkProg(fmt.Sprintf("meet%d", extra), vars, []string{"x"}, []string{"a"},
			map[string]int{"a": 5},
			blk("B0", nil, Branch(VarOp("x"), ">", ConstOp(0), "T", "E")),
			blk("T", nil, Jump("J")),
			blk("E", nil, Jump("J")),
			blk("J", []Instr{Check("a", ConstOp(1))}, Return()),
		)
	}
	few := mustAnalyze(t, build(0))
	many := mustAnalyze(t, build(500))
	if few.Stats.MeetOps != many.Stats.MeetOps {
		t.Fatalf("meet cost grew with unrelated variables: %d vs %d",
			few.Stats.MeetOps, many.Stats.MeetOps)
	}
}

func TestRemovedCountConsistent(t *testing.T) {
	r := mustAnalyze(t, loopProg("count", 0, 10, "<", 1, 10))
	removed := strings.Count(r.String(), ": REMOVE\n")
	if removed != r.Removed {
		t.Fatalf("report says removed=%d but %d REMOVE lines", r.Removed, removed)
	}
	kept := strings.Count(r.String(), ": KEEP\n")
	if kept+removed != len(r.Decisions) {
		t.Fatalf("kept+removed != total checks")
	}
}

// --- Cross-validation against the naive execution model ---

var cmpOps = []string{"<", "<=", ">", ">=", "==", "!="}

// genProgram builds a random but always valid program: a chain of
// straight-line, diamond and single-entry loop segments.
func genProgram(r *rand.Rand, id int) *Program {
	vars := []string{"i", "j", "k"}
	p := &Program{
		Name:    fmt.Sprintf("gen%d", id),
		Vars:    vars,
		Inputs:  []string{"j", "k"},
		Arrays:  []string{"a", "b"},
		Lengths: map[string]int{"a": 1 + r.Intn(8), "b": 1 + r.Intn(8)},
	}
	counter := 0
	name := func() string { counter++; return fmt.Sprintf("B%d", counter) }
	randVar := func() string { return vars[r.Intn(len(vars))] }
	randArr := func() string { return p.Arrays[r.Intn(len(p.Arrays))] }
	randIdx := func() Operand {
		if r.Intn(2) == 0 {
			return ConstOp(-2 + r.Intn(14))
		}
		return VarOp(randVar())
	}
	randInstrs := func(n int, loopVar string) []Instr {
		var out []Instr
		for m := 0; m < n; m++ {
			switch r.Intn(6) {
			case 0:
				out = append(out, AssignConst(randVar(), -2+r.Intn(12)))
			case 1:
				out = append(out, AssignAdd(randVar(), randVar(), -2+r.Intn(5)))
			case 2:
				out = append(out, Assign(randVar(), randVar()))
			case 3, 4:
				out = append(out, Check(randArr(), randIdx()))
			case 5:
				out = append(out, NewArray(randArr(), 1+r.Intn(8)))
			}
		}
		return out
	}

	const next = "__next__"
	var segments [][]*Block
	numSegs := 3 + r.Intn(4)
	for s := 0; s < numSegs; s++ {
		switch r.Intn(3) {
		case 0: // straight line
			b := name()
			segments = append(segments, []*Block{blk(b, randInstrs(r.Intn(4), ""), Jump(next))})
		case 1: // diamond
			h, t, e, j := name(), name(), name(), name()
			segments = append(segments, []*Block{
				blk(h, randInstrs(r.Intn(2), ""),
					Branch(VarOp(randVar()), cmpOps[r.Intn(len(cmpOps))], ConstOp(-2+r.Intn(12)), t, e)),
				blk(t, randInstrs(r.Intn(3), ""), Jump(j)),
				blk(e, randInstrs(r.Intn(3), ""), Jump(j)),
				blk(j, nil, Jump(next)),
			})
		case 2: // single-entry loop
			pre, h, body, exit := name(), name(), name(), name()
			up := r.Intn(2) == 0
			var guard Term
			var delta int
			if up {
				delta = 1 + r.Intn(2)
				op := "<"
				if r.Intn(2) == 0 {
					op = "<="
				}
				guard = Branch(VarOp("i"), op, ConstOp(r.Intn(12)), body, exit)
			} else {
				delta = -(1 + r.Intn(2))
				op := ">"
				if r.Intn(2) == 0 {
					op = ">="
				}
				guard = Branch(VarOp("i"), op, ConstOp(r.Intn(6)), body, exit)
			}
			start := r.Intn(12)
			if up && r.Intn(3) == 0 {
				start = -2 + r.Intn(4) // sometimes negative start
			}
			bodyInstrs := []Instr{}
			if r.Intn(3) != 0 {
				bodyInstrs = append(bodyInstrs, Check(randArr(), VarOp("i")))
			}
			if r.Intn(4) == 0 {
				bodyInstrs = append(bodyInstrs, randInstrs(1, "i")...) // messy body
			}
			bodyInstrs = append(bodyInstrs, AssignAdd("i", "i", delta))
			segments = append(segments, []*Block{
				blk(pre, []Instr{AssignConst("i", start)}, Jump(h)),
				blk(h, nil, guard),
				blk(body, bodyInstrs, Jump(h)),
				blk(exit, randInstrs(r.Intn(2), ""), Jump(next)),
			})
		}
	}
	end := name()
	for si, seg := range segments {
		target := end
		if si+1 < len(segments) {
			target = segments[si+1][0].Name
		}
		for _, b := range seg {
			for ti := range b.Terms {
				if b.Terms[ti].Kind == TJump && b.Terms[ti].Target == next {
					b.Terms[ti].Target = target
				}
			}
		}
		p.Blocks = append(p.Blocks, seg...)
	}
	p.Blocks = append(p.Blocks, blk(end, nil, Return()))
	p.Entry = segments[0][0].Name
	return p
}

// TestNaiveModelCrossCheck runs random programs with random inputs under
// the naive interpreter and verifies soundness: no removed check ever
// fails. Kept checks may or may not fail.
func TestNaiveModelCrossCheck(t *testing.T) {
	totalRemoved, totalRuns, totalFailed := 0, 0, 0
	for seed := int64(0); seed < 60; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := genProgram(r, int(seed))
		rep, err := Analyze(p)
		if err != nil {
			t.Fatalf("seed %d: generated program rejected: %v", seed, err)
		}
		removed := map[int]bool{}
		for _, d := range rep.Decisions {
			if d.Removed {
				removed[d.ID] = true
			}
		}
		totalRemoved += len(removed)
		for run := 0; run < 8; run++ {
			inputs := map[string]int{
				"j": -3 + r.Intn(14),
				"k": -3 + r.Intn(14),
			}
			out := Run(p, inputs, 100000)
			totalRuns++
			if out.FailedCheck >= 0 {
				totalFailed++
				if removed[out.FailedCheck] {
					t.Fatalf("seed %d run %d: removed check #%d failed at runtime\ninputs=%v\n%s",
						seed, run, out.FailedCheck, inputs, rep.String())
				}
			}
			t.Logf("seed=%d run=%d inputs=%v failedCheck=%d\n%s",
				seed, run, inputs, out.FailedCheck, rep.String())
		}
	}
	t.Logf("cross-check: %d runs, %d trapped on kept checks, %d removed checks total",
		totalRuns, totalFailed, totalRemoved)
	if totalRemoved == 0 {
		t.Fatalf("generator never produced a removable check; test is vacuous")
	}
}
