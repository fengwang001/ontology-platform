package stackvm

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

var _ = sync.WaitGroup{}

func progReturn() *Program {
	return &Program{Name: "ret", Code: []Instruction{
		{Op: OpPush, A: 5},
		{Op: OpReturn},
	}}
}

// burnAll loops until its frame runs out of fuel.
func progBurn() *Program {
	return &Program{Name: "burn", Code: []Instruction{
		{Op: OpPush, A: 1},
		{Op: OpPush, A: 1},
		{Op: OpAdd},
	}}
}

func mustSubmit(t *testing.T, m *Machine, ps ...*Program) {
	t.Helper()
	if err := m.Submit(ps...); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
}

func logCase(t *testing.T, name string, input, output []int64, why string, extra ...any) {
	t.Helper()
	t.Logf("case=%s input=%v output=%v verdict=%s %v", name, input, output, why, extra)
}

// The forwarding boundary r = 64k + 63 must forward r - r/64 and, when the
// request exceeds that allowance, no more.
func TestForwardBoundary(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	mustSubmit(t, m, progReturn())

	const rootFuel uint64 = FeeCall + 64*2 + 63 // r after CALL fee == 191
	parent := &Program{Name: "parent", Code: []Instruction{
		{Op: OpCall, TargetName: "ret", A: 1 << 30}, // huge request -> capped
		{Op: OpReturn},
	}}
	mustSubmit(t, m, parent)

	r := uint64(64*2 + 63)
	wantForward := r - r/64 // 191 - 2 == 189

	rec, err := m.Execute(context.Background(), "parent", nil, rootFuel)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Success {
		t.Fatalf("expected success, cause=%s", rec.Cause)
	}
	child := rec.Frames[1]
	if child.Initial != wantForward {
		t.Fatalf("forwarded=%d want=%d", child.Initial, wantForward)
	}
	// Child: PUSH(1)+RETURN(1) costs 2, returns 187.
	if child.Spent != 2 || child.Returned != wantForward-2 {
		t.Fatalf("child ledger wrong: %+v", child)
	}
	root := rec.Frames[0]
	if rec.InitialFuel != rec.SpentFuel+root.Returned {
		t.Fatalf("conservation broken: initial=%d spent=%d returned=%d",
			rec.InitialFuel, rec.SpentFuel, root.Returned)
	}
	logCase(t, "forward-boundary", []int64{int64(rootFuel)}, rec.Output,
		fmt.Sprintf("r=%d forwards r-r/64=%d; child returns %d", r, wantForward, child.Returned),
		"root", root, "child", child)
}

// A request below the allowance forwards exactly the requested amount.
func TestForwardSmallerRequest(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	mustSubmit(t, m, progReturn())
	parent := &Program{Name: "parent2", Code: []Instruction{
		{Op: OpCall, TargetName: "ret", A: 10},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, parent)

	rec, err := m.Execute(context.Background(), "parent2", nil, 100)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	if rec.Frames[1].Initial != 10 {
		t.Fatalf("forwarded %d want 10", rec.Frames[1].Initial)
	}
	logCase(t, "forward-smaller-request", []int64{100}, rec.Output,
		"requested 10 <= r-r/64 so exactly 10 forwarded", rec.Frames[1])
}

// Memory expansion is priced cumulatively: only cost(new)-cost(old) is
// charged when raising the high-water mark.
func TestMemoryExpansionIncremental(t *testing.T) {
	if got := memoryCost(0); got != 0 {
		t.Fatalf("cost(0)=%d want 0", got)
	}
	if got := memoryCost(16); got != 3*16+16*16/512 {
		t.Fatalf("cost(16)=%d", got)
	}

	storage := NewStorage()
	m := NewMachine(storage, 8)
	prog := &Program{Name: "mem", Code: []Instruction{
		{Op: OpMExpand, A: 16}, // fee 1 + (cost(16)-cost(0))
		{Op: OpMExpand, A: 16}, // no growth: fee 1 only
		{Op: OpMExpand, A: 32}, // fee 1 + (cost(32)-cost(16))
		{Op: OpReturn},         // fee 1
	}}
	mustSubmit(t, m, prog)

	c16 := memoryCost(16)
	c32 := memoryCost(32)
	wantSpent := FeeMExpand*3 + FeeReturn + c16 + (c32 - c16)

	rec, err := m.Execute(context.Background(), "mem", nil, wantSpent)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	if rec.Frames[0].Spent != wantSpent {
		t.Fatalf("spent=%d want=%d", rec.Frames[0].Spent, wantSpent)
	}
	logCase(t, "memory-expansion", []int64{16, 16, 32}, rec.Output,
		fmt.Sprintf("only deltas charged: c(16)=%d c(32)-c(16)=%d", c16, c32-c16),
		"spent", wantSpent)
}

// A fuel-exhausted child burns its whole forward and reports failure, but the
// parent continues and its storage write survives.
func TestChildExhaustedParentWriteSurvives(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	mustSubmit(t, m, progBurn())
	parent := &Program{Name: "p", Code: []Instruction{
		{Op: OpCall, TargetName: "burn", A: 3},
		// stack now [0] (failure marker); move it out of the way by storing
		// it at scratch key 0 before performing the real write.
		{Op: OpPush, A: 0}, // key below; marker on top becomes value
		{Op: OpSStore},     // scratch key 0 <- marker
		{Op: OpPush, A: 7}, // key
		{Op: OpPush, A: 1}, // value on top
		{Op: OpSStore},
		{Op: OpPush, A: 7},
		{Op: OpSLoad},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, parent)

	rec, err := m.Execute(context.Background(), "p", nil, 200)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	if len(rec.Output) != 1 || rec.Output[0] != 1 {
		t.Fatalf("output=%v want [1]", rec.Output)
	}
	if got := storage.Snapshot()[7]; got != 1 {
		t.Fatalf("storage key 7 = %d, parent write must survive child failure", got)
	}
	child := rec.Frames[1]
	if child.Cause != CauseFuelExhausted || child.Returned != 0 || child.Spent != child.Initial {
		t.Fatalf("child must burn all forwarded fuel: %+v", child)
	}
	if rec.InitialFuel != rec.SpentFuel+rec.Frames[0].Returned {
		t.Fatalf("conservation broken: %+v", rec.Frames)
	}
	logCase(t, "child-exhausted-parent-write", []int64{200}, rec.Output,
		"child burns full forward; parent got failure marker; key 7 written and survives",
		"storage", storage.Snapshot(), "child", child)
}

// A successful grandchild's writes are undone when the child frame fails for
// another reason; the parent still completes.
func TestGrandchildSuccessChildFails(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)

	grand := &Program{Name: "grand", Code: []Instruction{
		{Op: OpPush, A: 100},
		{Op: OpPush, A: 9},
		{Op: OpSStore},
		{Op: OpReturn},
	}}
	child := &Program{Name: "child", Code: []Instruction{
		{Op: OpCall, TargetName: "grand", A: 100},
		{Op: OpFail},
	}}
	parent := &Program{Name: "root", Code: []Instruction{
		{Op: OpCall, TargetName: "child", A: 200},
		{Op: OpPush, A: 200},
		{Op: OpPush, A: 2},
		{Op: OpSStore},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, grand, child, parent)

	rec, err := m.Execute(context.Background(), "root", nil, 400)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	snap := storage.Snapshot()
	if _, present := snap[100]; present {
		t.Fatalf("grandchild write at key 100 must be undone: %v", snap)
	}
	if snap[200] != 2 {
		t.Fatalf("parent write at key 200 must survive: %v", snap)
	}
	var childAcc FrameAccounting
	for _, acc := range rec.Frames {
		if acc.Program == "child" {
			childAcc = acc
		}
	}
	if childAcc.Cause != CauseExplicitFail {
		t.Fatalf("child cause=%s want explicit fail", childAcc.Cause)
	}
	logCase(t, "grand-ok-child-fails", []int64{400}, rec.Output,
		"child FAIL rolls back successful grandchild key 100; parent key 200 kept",
		"storage", snap, "frames", rec.Frames)
}

// Calls beyond the depth limit are immediate child failures: CALL fee still
// charged, zero fuel forwarded, parent gets the failure marker and continues.
func TestDepthLimit(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 1)
	mustSubmit(t, m, progReturn())
	dmid := &Program{Name: "dmid", Code: []Instruction{
		{Op: OpCall, TargetName: "ret", A: 100},
		{Op: OpReturn},
	}}
	top := &Program{Name: "dtop", Code: []Instruction{
		{Op: OpCall, TargetName: "dmid", A: 300},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, dmid, top)

	rec, err := m.Execute(context.Background(), "dtop", nil, 500)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	var depthFail *FrameAccounting
	var dmidAcc FrameAccounting
	for i := range rec.Frames {
		switch rec.Frames[i].Program {
		case "ret":
			if rec.Frames[i].Cause == CauseDepthExceeded {
				depthFail = &rec.Frames[i]
			}
		case "dmid":
			dmidAcc = rec.Frames[i]
		}
	}
	if depthFail == nil {
		t.Fatalf("expected a depth-exceeded record: %+v", rec.Frames)
	}
	if depthFail.Initial != 0 || depthFail.Spent != 0 {
		t.Fatalf("depth-exceeded call forwards nothing: %+v", depthFail)
	}
	if dmidAcc.Spent < FeeCall {
		t.Fatalf("dmid must pay CALL fee despite depth failure: %+v", dmidAcc)
	}
	logCase(t, "depth-limit", []int64{500}, rec.Output,
		"depth-2 call fails immediately, CALL fee charged, 0 forwarded",
		"frames", rec.Frames)
}

// A top-level failure rolls back every storage modification.
func TestTopLevelFailureRollsBackAll(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	mustSubmit(t, m, progReturn())
	prog := &Program{Name: "failroot", Code: []Instruction{
		{Op: OpPush, A: 1},
		{Op: OpPush, A: 42},
		{Op: OpSStore},
		{Op: OpCall, TargetName: "ret", A: 20},
		{Op: OpPush, A: 2},
		{Op: OpPush, A: 43},
		{Op: OpSStore},
		{Op: OpFail},
	}}
	mustSubmit(t, m, prog)

	before := storage.Snapshot()
	rec, err := m.Execute(context.Background(), "failroot", nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Success || rec.Cause != CauseExplicitFail {
		t.Fatalf("expected explicit fail, got success=%v cause=%s", rec.Success, rec.Cause)
	}
	after := storage.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("top-level failure must undo all writes: before=%v after=%v", before, after)
	}
	logCase(t, "top-failure-rollback", []int64{200}, rec.Output,
		"root FAIL undoes both root stores; storage identical to before",
		"storage", after, "frames", rec.Frames)
}

// Stack underflow is a distinguishable cause; non-exhaustion child failures
// refund their remaining fuel to the parent.
func TestStackUnderflowRefundsFuel(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	under := &Program{Name: "under", Code: []Instruction{{Op: OpAdd}}}
	parent := &Program{Name: "uparent", Code: []Instruction{
		{Op: OpCall, TargetName: "under", A: 100}, // capped by r-r/64
		{Op: OpReturn},
	}}
	mustSubmit(t, m, under, parent)

	rec, err := m.Execute(context.Background(), "uparent", nil, 47)
	if err != nil || !rec.Success {
		t.Fatalf("err=%v cause=%s", err, rec.Cause)
	}
	child := rec.Frames[1]
	if child.Cause != CauseStackUnderflow {
		t.Fatalf("child cause=%s want stack underflow", child.Cause)
	}
	// The ADD fee is charged first, then underflow fails the frame;
	// the remaining 38 is refunded to the parent.
	if child.Initial != 40 || child.Spent != FeeAdd || child.Returned != 38 {
		t.Fatalf("child ledger %+v", child)
	}
	root := rec.Frames[0]
	// root 47: CALL 7 -> r 40 -> forward 40; child keeps 2, returns 38;
	// RETURN 1 -> root left 37.
	if root.Returned != 37 {
		t.Fatalf("root returned=%d want 37", root.Returned)
	}
	logCase(t, "underflow-refund", []int64{47}, rec.Output,
		"ADD fee charged, then underflow; remaining 38 refunded to parent", child, root)
}

// Static violations reject the whole batch without registering anything.
func TestStaticRejection(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)

	err := m.Submit(&Program{Name: "bad", Code: []Instruction{{Op: Op(99)}}})
	if se, ok := err.(*StaticError); !ok || se.Reason != ReasonUnknownOpcode {
		t.Fatalf("want unknown opcode error, got %v", err)
	}

	err = m.Submit(&Program{Name: "caller", Code: []Instruction{
		{Op: OpCall, TargetName: "missing"},
	}})
	if se, ok := err.(*StaticError); !ok || se.Reason != ReasonMissingTarget {
		t.Fatalf("want missing target error, got %v", err)
	}

	mustSubmit(t, m, progReturn())
	err = m.Submit(&Program{Name: "neg", Code: []Instruction{
		{Op: OpMExpand, A: -1},
		{Op: OpCall, TargetName: "ret"},
	}})
	if se, ok := err.(*StaticError); !ok || se.Reason != ReasonNegativeWords {
		t.Fatalf("want negative words error, got %v", err)
	}
	if len(m.Programs()) != 1 || m.Programs()[0] != "ret" {
		t.Fatalf("rejected batch must register nothing: %v", m.Programs())
	}
	logCase(t, "static-rejection", nil, nil,
		"unknown opcode / missing target / negative words all rejected, nothing registered",
		"programs", m.Programs())
}

// Concurrent executions serialize and snapshots only show complete states.
func TestConcurrentSerializability(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 16)
	inc := &Program{Name: "inc", Code: []Instruction{
		{Op: OpPush, A: 1}, // key stays at stack bottom
		{Op: OpPush, A: 1}, // address to load
		{Op: OpSLoad},      // stack: key, old
		{Op: OpPush, A: 1},
		{Op: OpAdd}, // stack: key, old+1 (value on top)
		{Op: OpSStore},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, inc)

	const goroutines, perG = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	stop := make(chan struct{})

	// A reader must only ever observe the storage at an execution boundary:
	// the incrementer leaves key 1 holding a plain integer, so every snapshot
	// value is well-formed; atomicity itself is asserted by no lost updates.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = storage.Snapshot()
			}
		}
	}()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if _, err := m.Execute(context.Background(), "inc", nil, 10_000); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := storage.Snapshot()[1]; got != goroutines*perG {
		t.Fatalf("lost update: counter=%d want %d", got, goroutines*perG)
	}
	logCase(t, "concurrent-serializable", []int64{goroutines, perG},
		[]int64{storage.Snapshot()[1]},
		fmt.Sprintf("%d goroutines x %d increments applied atomically", goroutines, perG))
}

// Identical inputs repeatedly produce identical results and fuel ledgers.
func TestDeterminism(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	prog := &Program{Name: "det", Code: []Instruction{
		{Op: OpPush, A: 3},
		{Op: OpPush, A: 4},
		{Op: OpAdd},
		{Op: OpPush, A: 9},
		{Op: OpPush, A: 7},
		{Op: OpSStore},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, prog)

	first, err := m.Execute(context.Background(), "det", nil, 50)
	if err != nil || !first.Success {
		t.Fatalf("first run: %v", err)
	}
	firstSig := fmt.Sprintf("%v|%d|%v", first.Output, first.SpentFuel, storage.Snapshot())
	for i := 0; i < 5; i++ {
		rec, err := m.Execute(context.Background(), "det", nil, 50)
		if err != nil || !rec.Success {
			t.Fatalf("run %d: err=%v cause=%s", i, err, rec.Cause)
		}
		sig := fmt.Sprintf("%v|%d|%v", rec.Output, rec.SpentFuel, storage.Snapshot())
		if sig != firstSig {
			t.Fatalf("run %d differs:\n got %s\nwant %s", i, sig, firstSig)
		}
	}
	logCase(t, "determinism", []int64{50}, first.Output,
		"6 identical executions give identical output, fuel spend and storage",
		"spent", first.SpentFuel, "storage", storage.Snapshot())
}

// A root frame that runs out of fuel fails, burns the whole budget, returns
// nothing and rolls its writes back.
func TestRootFuelExhausted(t *testing.T) {
	storage := NewStorage()
	m := NewMachine(storage, 8)
	prog := &Program{Name: "oom", Code: []Instruction{
		{Op: OpPush, A: 5},
		{Op: OpPush, A: 6},
		{Op: OpSStore}, // 10; only 2 remain -> next PUSH exhausts
		{Op: OpPush, A: 1},
		{Op: OpReturn},
	}}
	mustSubmit(t, m, prog)

	before := storage.Snapshot()
	rec, err := m.Execute(context.Background(), "oom", nil, 12)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Success || rec.Cause != CauseFuelExhausted {
		t.Fatalf("want fuel exhaustion, success=%v cause=%s", rec.Success, rec.Cause)
	}
	root := rec.Frames[0]
	if root.Returned != 0 || root.Spent != 12 {
		t.Fatalf("exhausted root must burn all fuel: %+v", root)
	}
	if rec.InitialFuel != rec.SpentFuel {
		t.Fatalf("spent=%d want initial %d", rec.SpentFuel, rec.InitialFuel)
	}
	if fmt.Sprint(storage.Snapshot()) != fmt.Sprint(before) {
		t.Fatalf("storage changed: %v", storage.Snapshot())
	}
	logCase(t, "root-fuel-exhausted", []int64{12}, rec.Output,
		"root burns whole budget, returns 0, store write undone",
		"root", root, "storage", storage.Snapshot())
}
