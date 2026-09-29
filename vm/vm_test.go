package vm

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// logCase prints input, output and the decision basis for every test case.
func logCase(t *testing.T, name, input, got, basis string) {
	t.Helper()
	t.Logf("CASE %s\n  input : %s\n  output: %s\n  basis : %s", name, input, got, basis)
}

func indentReports(b *strings.Builder, r FrameReport) {
	fmt.Fprintf(b, "\n    depth=%d %q initial=%d consumed=%d sent=%d returned=%d reason=%s",
		r.Depth, r.Program, r.Initial, r.Consumed, r.SentToChildren, r.Returned, r.Reason)
	for _, c := range r.Children {
		indentReports(b, c)
	}
}

func resultString(r *Result, store *Store) string {
	var b strings.Builder
	fmt.Fprintf(&b, "success=%v reason=%s remaining=%d store=%v frames:",
		r.Success, r.Reason, r.Remaining, store.Snapshot())
	indentReports(&b, r.Root)
	return b.String()
}

// treeConsumed sums the own-consumption of every frame; forwarded fuel is
// counted by the frame that ultimately spends it, never twice.
func treeConsumed(r FrameReport) int64 {
	sum := r.Consumed
	for _, c := range r.Children {
		sum += treeConsumed(c)
	}
	return sum
}

// assertConservation checks the root frame identity and global conservation:
// initial == remaining + sum(frame.Consumed).
func assertConservation(t *testing.T, initial int64, r *Result) {
	t.Helper()
	if got := r.Root.Consumed + r.Root.SentToChildren + r.Root.Returned; got != r.Root.Initial {
		t.Fatalf("root identity broken: initial=%d consumed+sent+returned=%d", r.Root.Initial, got)
	}
	if spent := treeConsumed(r.Root); initial != r.Remaining+spent {
		t.Fatalf("global conservation broken: initial=%d remaining=%d treeConsumed=%d",
			initial, r.Remaining, spent)
	}
}

func push(v int64) Instruction   { return Instruction{Op: OpPush, Operand: v} }
func expand(n int64) Instruction { return Instruction{Op: OpExpand, Operand: n} }

func call(target, fuel int64) Instruction {
	return Instruction{Op: OpCall, A: target, B: fuel}
}

// TestForwardBoundary verifies forward = min(request, r - r/64) when the
// post-fee remainder r = 64k+63.
func TestForwardBoundary(t *testing.T) {
	// Child: PUSH 2 (value), PUSH 1 (key=top), STORE, RETURN -> 1+1+4+1 = 7.
	child := Program{Name: "child", Code: []Instruction{push(2), push(1), {Op: OpStore}, {Op: OpReturn}}}

	const k = 3 // r = FeeCall already paid; choose fuel so post-fee r = 64*3+63 = 255
	cases := []struct {
		name    string
		request int64
		sent    int64
		childOK bool
	}{
		{"r255-request-huge-caps-to-252", 1000, 252, true},
		{"r255-request-252-exact", 252, 252, true},
		{"r255-request-253-capped", 253, 252, true},
		{"r255-request-10-as-is", 10, 10, true},
		{"r255-request-5-insufficient", 5, 5, false},
	}

	for _, tc := range cases {
		store := NewStore()
		parent := Program{Name: "parent", Code: []Instruction{call(1, tc.request), {Op: OpReturn}}}
		exec, err := NewExecutor(store, Programs{parent, child})
		if err != nil {
			t.Fatalf("unexpected static error: %v", err)
		}
		fuel := FeeCall + 64*k + 63 // post-CALL-fee remainder r = 255
		res, err := exec.Run(0, fuel)
		if err != nil {
			t.Fatalf("run error: %v", err)
		}
		basis := fmt.Sprintf("r=255, forward=min(%d,255-255/64=252)=%d; child needs 7; childOK=%v",
			tc.request, tc.sent, tc.childOK)
		logCase(t, "forward/"+tc.name, fmt.Sprintf("fuel=%d request=%d", fuel, tc.request),
			resultString(res, store), basis)

		if !res.Success {
			t.Fatalf("%s: parent must keep running regardless of child outcome", tc.name)
		}
		kid := res.Root.Children[0]
		if kid.Initial != tc.sent {
			t.Fatalf("%s: forwarded %d want %d", tc.name, kid.Initial, tc.sent)
		}
		if (kid.Reason == FailNone) != tc.childOK {
			t.Fatalf("%s: child success mismatch, reason=%s", tc.name, kid.Reason)
		}
		if _, ok := store.Snapshot()[1]; ok != tc.childOK {
			t.Fatalf("%s: child store presence=%v want %v", tc.name, ok, tc.childOK)
		}
		assertConservation(t, fuel, res)
	}
}

// TestMemoryExpansionPricing charges only the high-water-mark delta and
// verifies cost(n) = 3n + n*n/512 at 64 and 128 words.
func TestMemoryExpansionPricing(t *testing.T) {
	p := Program{Name: "mem", Code: []Instruction{expand(64), expand(128), {Op: OpReturn}}}

	cost64 := memoryCost(64)                                                // 192 + 8 = 200
	cost128 := memoryCost(128)                                              // 384 + 32 = 416
	want := FeeExpand + cost64 + FeeExpand + (cost128 - cost64) + FeeReturn // 421

	store := NewStore()
	exec, _ := NewExecutor(store, Programs{p})
	res, err := exec.Run(0, want)
	if err != nil {
		t.Fatal(err)
	}
	basis := fmt.Sprintf("cost(64)=%d cost(128)=%d delta=%d; only new high-water delta charged; consumed=%d",
		cost64, cost128, cost128-cost64, want)
	logCase(t, "memory-expansion", fmt.Sprintf("expansions=[64,128] fuel=%d", want),
		resultString(res, store), basis)

	if !res.Success || res.Root.Consumed != want || res.Remaining != 0 {
		t.Fatalf("want success consumed=%d/0, got %s consumed=%d", want, res.Reason, res.Root.Consumed)
	}

	store2 := NewStore()
	exec2, _ := NewExecutor(store2, Programs{p})
	res2, _ := exec2.Run(0, want-1)
	logCase(t, "memory-expansion-short", fmt.Sprintf("fuel=%d", want-1),
		resultString(res2, store2), "one unit short -> FailOutOfFuel; residual burned to keep identity exact")
	if res2.Success || res2.Reason != FailOutOfFuel || res2.Remaining != 0 {
		t.Fatalf("want out-of-fuel with 0 remaining, got %s %d", res2.Reason, res2.Remaining)
	}
	assertConservation(t, want, res)
	assertConservation(t, want-1, res2)
}

// TestChildExhaustParentWriteSurvives: child burns the whole forwarded
// budget; the parent keeps executing and its own write is committed.
func TestChildExhaustParentWriteSurvives(t *testing.T) {
	var childCode []Instruction
	childCode = append(childCode, push(0), push(0))
	for i := 0; i < 30; i++ {
		childCode = append(childCode, push(1), Instruction{Op: OpAdd})
	}
	child := Program{Name: "burner", Code: childCode}
	// Parent: CALL child(50), then write key1=9, RETURN.
	parent := Program{Name: "parent", Code: []Instruction{
		call(1, 50),
		push(9), push(1), {Op: OpStore},
		{Op: OpReturn},
	}}

	store := NewStore()
	exec, _ := NewExecutor(store, Programs{parent, child})
	const fuel int64 = 500
	res, err := exec.Run(0, fuel)
	if err != nil {
		t.Fatal(err)
	}
	basis := "child receives 50 and exhausts it (PUSH/ADD loop); all 50 stay spent, child has no writes; parent receives failure flag, continues, commits key1=9"
	logCase(t, "child-exhaust-parent-write", fmt.Sprintf("fuel=%d forward=50", fuel),
		resultString(res, store), basis)

	if !res.Success {
		t.Fatalf("parent must succeed, got %s", res.Reason)
	}
	if res.Root.Children[0].Reason != FailOutOfFuel || res.Root.Children[0].Returned != 0 {
		t.Fatalf("child must exhaust and return 0 fuel: %+v", res.Root.Children[0])
	}
	if store.Get(1) != 9 {
		t.Fatalf("parent write lost, store=%v", store.Snapshot())
	}
	assertConservation(t, fuel, res)
}

// TestGrandchildSuccessChildFails: a successful grandchild's writes live
// inside the child layer; when the child later FAILs they are discarded with
// it, while parent writes on both sides of the call survive.
func TestGrandchildSuccessChildFails(t *testing.T) {
	grand := Program{Name: "grand", Code: []Instruction{
		push(77), push(3), {Op: OpStore}, {Op: OpReturn},
	}}
	child := Program{Name: "child", Code: []Instruction{call(2, 100), {Op: OpFail}}}
	parent := Program{Name: "parent", Code: []Instruction{
		push(5), push(2), {Op: OpStore},
		call(1, 100),
		push(6), push(4), {Op: OpStore},
		{Op: OpReturn},
	}}

	store := NewStore()
	exec, _ := NewExecutor(store, Programs{parent, child, grand})
	const fuel int64 = 1000
	res, err := exec.Run(0, fuel)
	if err != nil {
		t.Fatal(err)
	}
	basis := "grand succeeds and key3=77 merges into child; child FAILs -> its whole layer incl. grand writes is dropped and unused fuel refunded; parent keeps key2=5 and key4=6"
	logCase(t, "grand-ok-child-fail", fmt.Sprintf("fuel=%d", fuel),
		resultString(res, store), basis)

	if !res.Success {
		t.Fatalf("parent must survive child failure, got %s", res.Reason)
	}
	snap := store.Snapshot()
	if snap[2] != 5 || snap[4] != 6 {
		t.Fatalf("parent writes missing: %v", snap)
	}
	if _, ok := snap[3]; ok {
		t.Fatalf("grandchild write must be undone with the failed child: %v", snap)
	}
	if res.Root.Children[0].Reason != FailExplicit {
		t.Fatalf("child reason=%s want explicit-fail", res.Root.Children[0].Reason)
	}
	if res.Root.Children[0].Children[0].Reason != FailNone {
		t.Fatal("grandchild must have succeeded")
	}
	assertConservation(t, fuel, res)
}

// TestDepthLimit: a recursive chain bottoms out at MaxCallDepth; the next
// CALL is an instant child failure (fee charged, nothing forwarded).
func TestDepthLimit(t *testing.T) {
	prog := Program{Name: "rec", Code: []Instruction{call(0, 100000), push(1), {Op: OpReturn}}}
	store := NewStore()
	exec, _ := NewExecutor(store, Programs{prog})
	const fuel int64 = 1000
	res, err := exec.Run(0, fuel)
	if err != nil {
		t.Fatal(err)
	}
	basis := fmt.Sprintf("frames descend to depth %d; the CALL at depth %d pays FeeCall, forwards nothing and reports FailDepthExceeded; ancestors continue and succeed",
		MaxCallDepth, MaxCallDepth)
	logCase(t, "depth-limit", fmt.Sprintf("fuel=%d maxDepth=%d", fuel, MaxCallDepth),
		resultString(res, store), basis)

	if !res.Success {
		t.Fatalf("depth-exceeded callee must not fail ancestors, got %s", res.Reason)
	}
	r := res.Root
	for d := 1; d <= MaxCallDepth; d++ {
		if r.Depth != d || len(r.Children) != 1 {
			t.Fatalf("depth %d report malformed: %+v", d, r)
		}
		r = r.Children[0]
	}
	if r.Depth != MaxCallDepth+1 || r.Reason != FailDepthExceeded || r.Initial != 0 {
		t.Fatalf("synthetic depth-exceeded child malformed: %+v", r)
	}
	assertConservation(t, fuel, res)
}

// TestTopLevelFailureRollsAllBack: any failure at the root frame, even after
// a successful child commit into its layer, undoes the entire execution.
func TestTopLevelFailureRollsAllBack(t *testing.T) {
	good := Program{Name: "good", Code: []Instruction{push(42), push(7), {Op: OpStore}, {Op: OpReturn}}}
	root := Program{Name: "root", Code: []Instruction{
		call(1, 100),
		push(8), push(9), {Op: OpStore},
		{Op: OpFail},
	}}

	store := NewStore()
	exec, _ := NewExecutor(store, Programs{root, good})
	const fuel int64 = 500
	res, err := exec.Run(0, fuel)
	if err != nil {
		t.Fatal(err)
	}
	basis := "child succeeds (key7=42) then root writes key9=8 then FAILs; root failure discards its whole layer including the merged child writes; nothing commits"
	logCase(t, "top-level-rollback", fmt.Sprintf("fuel=%d", fuel),
		resultString(res, store), basis)

	if res.Success || res.Reason != FailExplicit {
		t.Fatalf("want explicit-fail root, got success=%v reason=%s", res.Success, res.Reason)
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("top-level failure must roll back everything, store=%v", store.Snapshot())
	}
	assertConservation(t, fuel, res)

	// Stack underflow is a distinct runtime reason and also rolls back.
	store2 := NewStore()
	bad := Program{Name: "underflow", Code: []Instruction{push(1), {Op: OpAdd}, {Op: OpReturn}}}
	exec2, _ := NewExecutor(store2, Programs{bad})
	res2, _ := exec2.Run(0, 100)
	logCase(t, "stack-underflow", "fuel=100 code=PUSH1,ADD",
		resultString(res2, store2), "ADD with one element -> FailStackUnderflow, no store change")
	if res2.Success || res2.Reason != FailStackUnderflow || len(store2.Snapshot()) != 0 {
		t.Fatalf("want stack-underflow with empty store, got %+v", res2)
	}
}

// TestStaticRejection rejects the whole program set before any execution.
func TestStaticRejection(t *testing.T) {
	base := []Program{{Name: "ok", Code: []Instruction{push(1), {Op: OpReturn}}}}
	cases := []struct {
		name  string
		progs Programs
	}{
		{"unknown-opcode", Programs{{Name: "x", Code: []Instruction{{Op: Opcode(99)}}}}},
		{"missing-call-target", Programs{base[0], {Name: "c", Code: []Instruction{call(5, 10)}}}},
		{"negative-expand", Programs{{Name: "x", Code: []Instruction{expand(-1)}}}},
		{"negative-call-fuel", Programs{base[0], {Name: "c", Code: []Instruction{call(0, -3)}}}},
	}
	for _, tc := range cases {
		store := NewStore()
		_, err := NewExecutor(store, tc.progs)
		logCase(t, "static/"+tc.name, "programs with illegal construct",
			fmt.Sprintf("err=%v store=%v", err, store.Snapshot()), "Validate rejects before execution; nothing runs")
		if err == nil {
			t.Fatalf("%s: expected static rejection", tc.name)
		}
		if _, ok := err.(*StaticError); !ok {
			t.Fatalf("%s: want *StaticError, got %T", tc.name, err)
		}
	}
}

// TestConcurrentSubmitAndDeterminism: executions submitted concurrently to
// one store serialize and each appears atomic; identical inputs replay to
// identical outputs.
func TestConcurrentSubmitAndDeterminism(t *testing.T) {
	// Each program writes two keys in one execution, so a reader must never
	// observe exactly one key of a pair (half-applied execution).
	writePair := func(a, b int64) Program {
		return Program{Name: "pair", Code: []Instruction{
			push(a), push(a), {Op: OpStore},
			push(b), push(b), {Op: OpStore},
			{Op: OpReturn},
		}}
	}
	progs := Programs{writePair(10, 20), writePair(30, 40)} // keys 10/20 vs 30/40
	store := NewStore()

	var readers sync.WaitGroup
	var runners sync.WaitGroup
	stop := make(chan struct{})
	readers.Add(1)
	go func() { // reader: only complete pre/post states
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap := store.Snapshot()
				_, k10 := snap[10]
				_, k20 := snap[20]
				_, k30 := snap[30]
				_, k40 := snap[40]
				if k10 != k20 || k30 != k40 {
					t.Errorf("reader observed half-applied execution: %v", snap)
					return
				}
			}
		}
	}()

	for round := 0; round < 50; round++ {
		runners.Add(2)
		for prog := 0; prog < 2; prog++ {
			go func(target int) {
				defer runners.Done()
				exec, err := NewExecutor(store, progs)
				if err != nil {
					t.Error(err)
					return
				}
				res, err := exec.Run(target, 1000)
				if err != nil || !res.Success {
					t.Errorf("run failed: %v %+v", err, res)
				}
			}(prog)
		}
		runners.Wait()
	}
	close(stop)
	readers.Wait()

	snap := store.Snapshot()
	logCase(t, "concurrent", "50 rounds x 2 programs writing key pairs on one store",
		fmt.Sprintf("final=%v", snap), "write lock serializes executions; readers never see partial pairs; both programs eventually visible")
	if snap[10] != 10 || snap[20] != 20 || snap[30] != 30 || snap[40] != 40 {
		t.Fatalf("unexpected final store: %v", snap)
	}

	// Determinism: replay the same input on fresh stores, compare outcomes.
	var reference *Result
	var refStore map[int64]int64
	prog := Programs{writePair(7, 8)}
	for i := 0; i < 20; i++ {
		s := NewStore()
		exec, _ := NewExecutor(s, prog)
		res, _ := exec.Run(0, 300)
		if reference == nil {
			reference, refStore = res, s.Snapshot()
			continue
		}
		if res.Remaining != reference.Remaining || res.Reason != reference.Reason ||
			fmt.Sprint(s.Snapshot()) != fmt.Sprint(refStore) {
			t.Fatalf("non-deterministic replay at %d: %+v store=%v vs %+v store=%v",
				i, res, s.Snapshot(), reference, refStore)
		}
	}
	logCase(t, "determinism", "same program+fuel replayed 20 times",
		fmt.Sprintf("remaining=%d reason=%s store=%v", reference.Remaining, reference.Reason, refStore),
		"identical inputs produce identical remaining fuel, reason and storage")
}
