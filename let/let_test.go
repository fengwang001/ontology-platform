package let

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// mustAddTask registers a task and fails the test on error.
func mustAddTask(t *testing.T, a *Analyzer, id string, period, phi, w int) {
	t.Helper()
	if err := a.AddTask(Task{ID: id, T: period, Phi: phi, W: w}); err != nil {
		t.Fatalf("AddTask(%s): %v", id, err)
	}
}

// mustAddChain defines a chain and fails the test on error.
func mustAddChain(t *testing.T, a *Analyzer, name string, ids ...string) {
	t.Helper()
	if err := a.AddChain(name, ids); err != nil {
		t.Fatalf("AddChain(%s): %v", name, err)
	}
}

// exampleAnalyzer builds the running example: A(T=4,phi=0,w=4),
// B(T=6,phi=phiB,w=6), chain "c" = [A,B].
func exampleAnalyzer(t *testing.T, phiB int) *Analyzer {
	t.Helper()
	a := NewAnalyzer()
	mustAddTask(t, a, "A", 4, 0, 4)
	mustAddTask(t, a, "B", 6, phiB, 6)
	mustAddChain(t, a, "c", "A", "B")
	return a
}

func TestExampleBaseline(t *testing.T) {
	a := exampleAnalyzer(t, 1)
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := Result{MaxReaction: 18, MinReaction: 11, MaxAge: 18}
	if res != want {
		t.Fatalf("Analyze = %+v, want %+v", res, want)
	}
}

func TestExamplePhaseZero(t *testing.T) {
	a := exampleAnalyzer(t, 0)
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := Result{MaxReaction: 17, MinReaction: 10, MaxAge: 17}
	if res != want {
		t.Fatalf("Analyze = %+v, want %+v", res, want)
	}
}

// TestSameInstantVisibility: a value written at u is visible to a read
// released at exactly u. A(T=2,phi=0,w=2) writes at 2,4,6,...;
// B(T=2,phi=0,w=2) reads at 0,2,4,.... For x=1 the sample is taken at 2,
// written at 4, and B's job released at 4 must see it.
func TestSameInstantVisibility(t *testing.T) {
	chain := []Task{
		{ID: "A", T: 2, Phi: 0, W: 2},
		{ID: "B", T: 2, Phi: 0, W: 2},
	}
	if got := reactionAt(chain, 1); got != 5 {
		t.Fatalf("Reaction(1) = %d, want 5 (write at 4 read at 4)", got)
	}
	// x=5 in the tuned example: A samples at 8, writes at 12, B releases
	// at 12 and reads the value at the same instant.
	example := []Task{
		{ID: "A", T: 4, Phi: 0, W: 4},
		{ID: "B", T: 6, Phi: 0, W: 6},
	}
	if got := reactionAt(example, 5); got != 13 {
		t.Fatalf("Reaction(5) = %d, want 13 (write at 12 read at 12)", got)
	}
}

// TestOffByOneInvisible: a read released one time unit before the write
// must not see the value. A(T=4,phi=0,w=4) writes at 4,8,12,...;
// B(T=7,phi=0,w=7) reads at 0,7,14,.... For x=1 the sample is written at
// 8, missing B's release at 7 by one, so B's next release is 14.
func TestOffByOneInvisible(t *testing.T) {
	chain := []Task{
		{ID: "A", T: 4, Phi: 0, W: 4},
		{ID: "B", T: 7, Phi: 0, W: 7},
	}
	if got := reactionAt(chain, 0); got != 14 {
		t.Fatalf("Reaction(0) = %d, want 14", got)
	}
	if got := reactionAt(chain, 1); got != 20 {
		t.Fatalf("Reaction(1) = %d, want 20 (write at 8 misses read at 7)", got)
	}
}

// TestSampleExactlyAtX: a job of tau1 released exactly at x counts as the
// sample for x. A(T=4,phi=2,w=2) releases at 2,6,10,....
func TestSampleExactlyAtX(t *testing.T) {
	chain := []Task{
		{ID: "A", T: 4, Phi: 2, W: 2},
		{ID: "B", T: 3, Phi: 0, W: 1},
	}
	// x=2: r1=2 (not 6), t1=4, r2=6, t2=7.
	if got := reactionAt(chain, 2); got != 5 {
		t.Fatalf("Reaction(2) = %d, want 5 (release at x counts)", got)
	}
}

// TestImplicitCommunication covers w < T (fixed response time).
func TestImplicitCommunication(t *testing.T) {
	a := NewAnalyzer()
	mustAddTask(t, a, "A", 10, 0, 2)
	mustAddTask(t, a, "B", 10, 0, 3)
	mustAddChain(t, a, "c", "A", "B")
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := Result{MaxReaction: 22, MinReaction: 13, MaxAge: 22}
	if res != want {
		t.Fatalf("Analyze = %+v, want %+v", res, want)
	}
}

// TestHyperperiodCoprime: periods 3 and 5 give H = 15, and Reaction/Age
// are H-periodic.
func TestHyperperiodCoprime(t *testing.T) {
	chain := []Task{
		{ID: "A", T: 3, Phi: 0, W: 3},
		{ID: "B", T: 5, Phi: 1, W: 2},
	}
	if h := hyperperiod(chain); h != 15 {
		t.Fatalf("H = %d, want 15", h)
	}
	phi := maxPhase(chain)
	h := hyperperiod(chain)
	for x := phi; x < phi+h; x++ {
		if reactionAt(chain, x) != reactionAt(chain, x+h) {
			t.Fatalf("Reaction(%d) != Reaction(%d)", x, x+h)
		}
	}
	w := phi + 2*sumPeriods(chain)
	for x := w; x < w+h; x++ {
		if ageAt(chain, x) != ageAt(chain, x+h) {
			t.Fatalf("Age(%d) != Age(%d)", x, x+h)
		}
	}
}

// TestIntervalEndpoints: sweeping [Phi, Phi+H) covers every residue; the
// point Phi+H repeats Phi, and likewise for the age interval [W, W+H).
func TestIntervalEndpoints(t *testing.T) {
	chain := []Task{
		{ID: "A", T: 4, Phi: 0, W: 4},
		{ID: "B", T: 6, Phi: 1, W: 6},
	}
	phi, h := maxPhase(chain), hyperperiod(chain)
	if reactionAt(chain, phi) != reactionAt(chain, phi+h) {
		t.Fatal("Reaction(Phi) != Reaction(Phi+H)")
	}
	maxOpen, maxClosed := 0, 0
	for x := phi; x < phi+h; x++ {
		maxOpen = max(maxOpen, reactionAt(chain, x))
	}
	for x := phi; x <= phi+h; x++ {
		maxClosed = max(maxClosed, reactionAt(chain, x))
	}
	if maxOpen != maxClosed {
		t.Fatalf("max over [Phi,Phi+H) = %d, over [Phi,Phi+H] = %d", maxOpen, maxClosed)
	}
	w := phi + 2*sumPeriods(chain)
	if ageAt(chain, w) != ageAt(chain, w+h) {
		t.Fatal("Age(W) != Age(W+H)")
	}
}

// TestTuneCommits: tuning the example with phi_B=1 finds phi_B=0 (the
// lexicographically smallest vector among the tied optima {0, 2}) and
// commits because 17 < 18.
func TestTuneCommits(t *testing.T) {
	a := exampleAnalyzer(t, 1)
	tr, err := a.Tune("c")
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if !tr.Changed {
		t.Fatal("Tune did not commit")
	}
	if tr.MaxReactionBefore != 18 || tr.MaxReactionAfter != 17 {
		t.Fatalf("Tune before/after = %d/%d, want 18/17",
			tr.MaxReactionBefore, tr.MaxReactionAfter)
	}
	if !reflect.DeepEqual(tr.PhasesBefore, []int{0, 1}) {
		t.Fatalf("PhasesBefore = %v, want [0 1]", tr.PhasesBefore)
	}
	if !reflect.DeepEqual(tr.PhasesAfter, []int{0, 0}) {
		t.Fatalf("PhasesAfter = %v, want [0 0] (lex smallest tie)", tr.PhasesAfter)
	}
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MaxReaction != 17 || res.MinReaction != 10 || res.MaxAge != 17 {
		t.Fatalf("post-Tune Analyze = %+v, want {17 10 17}", res)
	}
	// Tune must not change w or chain composition.
	if err := a.SetDelay("B", 6); err != nil {
		t.Fatalf("w changed by Tune: %v", err)
	}
	if err := a.RemoveTask("B"); !errors.Is(err, ErrInUse) {
		t.Fatalf("chain composition changed by Tune: %v", err)
	}
}

// TestTuneTieLexSmallest: phi_B=0 and phi_B=2 both yield MaxReaction 17;
// Tune must pick 0.
func TestTuneTieLexSmallest(t *testing.T) {
	a := exampleAnalyzer(t, 2)
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MaxReaction != 17 {
		t.Fatalf("MaxReaction with phi_B=2 = %d, want 17 (tie with phi_B=0)", res.MaxReaction)
	}
	b := exampleAnalyzer(t, 1)
	tr, err := b.Tune("c")
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if !reflect.DeepEqual(tr.PhasesAfter, []int{0, 0}) {
		t.Fatalf("Tune picked %v, want lex smallest [0 0]", tr.PhasesAfter)
	}
}

// TestTuneAlreadyOptimal: with phi_B=2 the current MaxReaction 17 is
// already optimal, so Tune must not change anything and the "after"
// values equal the "before" values.
func TestTuneAlreadyOptimal(t *testing.T) {
	a := exampleAnalyzer(t, 2)
	tr, err := a.Tune("c")
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if tr.Changed {
		t.Fatal("Tune changed an already optimal chain")
	}
	if tr.MaxReactionBefore != 17 || tr.MaxReactionAfter != 17 {
		t.Fatalf("before/after = %d/%d, want 17/17",
			tr.MaxReactionBefore, tr.MaxReactionAfter)
	}
	if !reflect.DeepEqual(tr.PhasesAfter, []int{0, 2}) {
		t.Fatalf("PhasesAfter = %v, want unchanged [0 2]", tr.PhasesAfter)
	}
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MaxReaction != 17 {
		t.Fatalf("MaxReaction = %d after no-op Tune, want 17", res.MaxReaction)
	}
}

// TestTuneEquivalentToSetPhase: committing a Tune result is equivalent to
// applying the same phases via SetPhase calls.
func TestTuneEquivalentToSetPhase(t *testing.T) {
	a := exampleAnalyzer(t, 1)
	tr, err := a.Tune("c")
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	b := exampleAnalyzer(t, 1)
	for i, id := range []string{"A", "B"} {
		if err := b.SetPhase(id, tr.PhasesAfter[i]); err != nil {
			t.Fatalf("SetPhase(%s): %v", id, err)
		}
	}
	ra, err1 := a.Analyze("c")
	rb, err2 := b.Analyze("c")
	if err1 != nil || err2 != nil {
		t.Fatalf("Analyze errors: %v %v", err1, err2)
	}
	if ra != rb {
		t.Fatalf("Tune result %+v != SetPhase result %+v", ra, rb)
	}
}

// TestTuneShared: a chain sharing any task with another chain is rejected.
func TestTuneShared(t *testing.T) {
	a := NewAnalyzer()
	mustAddTask(t, a, "A", 4, 0, 4)
	mustAddTask(t, a, "B", 6, 1, 6)
	mustAddTask(t, a, "C", 5, 0, 5)
	mustAddChain(t, a, "c1", "A", "B")
	mustAddChain(t, a, "c2", "A", "C")
	if _, err := a.Tune("c1"); !errors.Is(err, ErrShared) {
		t.Fatalf("Tune(c1) = %v, want ErrShared", err)
	}
	if _, err := a.Tune("c2"); !errors.Is(err, ErrShared) {
		t.Fatalf("Tune(c2) = %v, want ErrShared", err)
	}
	// Rejected Tune must not change anything.
	res, err := a.Analyze("c1")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MaxReaction != 18 {
		t.Fatalf("MaxReaction = %d after rejected Tune, want 18", res.MaxReaction)
	}
	// After removing the other chain, Tune succeeds.
	if err := a.RemoveChain("c2"); err != nil {
		t.Fatalf("RemoveChain: %v", err)
	}
	if _, err := a.Tune("c1"); err != nil {
		t.Fatalf("Tune after RemoveChain: %v", err)
	}
}

// TestTuneTooLarge: the product of T2..Tn must not exceed 1000.
func TestTuneTooLarge(t *testing.T) {
	a := NewAnalyzer()
	mustAddTask(t, a, "A", 1, 0, 1)
	mustAddTask(t, a, "B", 40, 0, 40)
	mustAddTask(t, a, "C", 40, 0, 40)
	mustAddChain(t, a, "c", "A", "B", "C") // H = 40, product = 1600
	if _, err := a.Tune("c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Tune = %v, want ErrTooLarge", err)
	}
}

// TestAddChainTooLarge: H above 5000 is rejected at chain creation.
func TestAddChainTooLarge(t *testing.T) {
	a := NewAnalyzer()
	mustAddTask(t, a, "A", 997, 0, 1)
	mustAddTask(t, a, "B", 1000, 0, 1)
	if err := a.AddChain("c", []string{"A", "B"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("AddChain = %v, want ErrTooLarge", err)
	}
}

// TestRejectionOrder checks that the first applicable rejection reason is
// reported and that rejected operations leave the state untouched.
func TestRejectionOrder(t *testing.T) {
	a := NewAnalyzer()
	longID := string(make([]byte, 33))
	invalid := []Task{
		{ID: "", T: 4, Phi: 0, W: 1},     // empty ID
		{ID: longID, T: 4, Phi: 0, W: 1}, // ID too long
		{ID: "x", T: 0, Phi: 0, W: 1},    // period too small
		{ID: "x", T: 1001, Phi: 0, W: 1}, // period too large
		{ID: "x", T: 4, Phi: -1, W: 1},   // negative phase
		{ID: "x", T: 4, Phi: 4, W: 1},    // phase >= T
		{ID: "x", T: 4, Phi: 0, W: 0},    // write delay too small
		{ID: "x", T: 4, Phi: 0, W: 5},    // write delay > T
	}
	for _, task := range invalid {
		if err := a.AddTask(task); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("AddTask(%+v) = %v, want ErrInvalidArgument", task, err)
		}
	}
	mustAddTask(t, a, "A", 4, 0, 4)
	// Invalid argument beats duplicate.
	dup := Task{ID: "A", T: 0, Phi: 0, W: 1}
	if err := a.AddTask(dup); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddTask(invalid dup) = %v, want ErrInvalidArgument", err)
	}
	if err := a.AddTask(Task{ID: "A", T: 4, Phi: 0, W: 1}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("AddTask(dup) = %v, want ErrDuplicate", err)
	}
	// Duplicate beats capacity: fill to 16 tasks (A plus 15 more).
	for i := 0; i < 15; i++ {
		mustAddTask(t, a, string(rune('a'+i)), 2, 0, 1)
	}
	if err := a.AddTask(Task{ID: "A", T: 4, Phi: 0, W: 1}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("AddTask(dup at capacity) = %v, want ErrDuplicate", err)
	}
	if err := a.AddTask(Task{ID: "z", T: 4, Phi: 0, W: 1}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("AddTask(17th) = %v, want ErrCapacity", err)
	}
	// Not found.
	if err := a.RemoveTask("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveTask(missing) = %v, want ErrNotFound", err)
	}
	if err := a.SetPhase("nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetPhase(missing) = %v, want ErrNotFound", err)
	}
	if err := a.SetDelay("nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDelay(missing) = %v, want ErrNotFound", err)
	}
	// Invalid argument beats not found.
	if err := a.SetPhase("", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetPhase(empty id) = %v, want ErrInvalidArgument", err)
	}
	if err := a.SetPhase("nope", -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetPhase(negative) = %v, want ErrInvalidArgument", err)
	}
	if err := a.SetPhase("A", 4); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetPhase(>=T) = %v, want ErrInvalidArgument", err)
	}
	if err := a.SetDelay("A", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDelay(0) = %v, want ErrInvalidArgument", err)
	}
	if err := a.SetDelay("A", 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDelay(>T) = %v, want ErrInvalidArgument", err)
	}
	// Chain validation order: invalid -> not found -> duplicate.
	if err := a.AddChain("", []string{"A", "b"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddChain(empty name) = %v, want ErrInvalidArgument", err)
	}
	if err := a.AddChain("c", []string{"A"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddChain(len 1) = %v, want ErrInvalidArgument", err)
	}
	if err := a.AddChain("c", []string{"A", "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddChain(unknown task) = %v, want ErrNotFound", err)
	}
	if err := a.AddChain("c", []string{"A", "A"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("AddChain(dup task) = %v, want ErrDuplicate", err)
	}
	mustAddChain(t, a, "c", "A", "b")
	if err := a.AddChain("c", []string{"A", "d"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("AddChain(dup name) = %v, want ErrDuplicate", err)
	}
	// Task in use cannot be removed; rejected ops change nothing.
	if err := a.RemoveTask("A"); !errors.Is(err, ErrInUse) {
		t.Fatalf("RemoveTask(in use) = %v, want ErrInUse", err)
	}
	if err := a.RemoveChain("c"); err != nil {
		t.Fatalf("RemoveChain: %v", err)
	}
	if err := a.RemoveTask("A"); err != nil {
		t.Fatalf("RemoveTask after RemoveChain: %v", err)
	}
	if _, err := a.Analyze("c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Analyze(removed) = %v, want ErrNotFound", err)
	}
	if _, err := a.Analyze(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Analyze(empty) = %v, want ErrInvalidArgument", err)
	}
	if _, err := a.Tune(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Tune(empty) = %v, want ErrInvalidArgument", err)
	}
	if _, err := a.Tune("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tune(missing) = %v, want ErrNotFound", err)
	}
}

// TestRejectedSetPhaseKeepsState: a rejected SetPhase/SetDelay must not
// alter analysis results.
func TestRejectedSetPhaseKeepsState(t *testing.T) {
	a := exampleAnalyzer(t, 1)
	before, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if err := a.SetPhase("B", 6); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetPhase = %v, want ErrInvalidArgument", err)
	}
	if err := a.SetDelay("B", 7); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDelay = %v, want ErrInvalidArgument", err)
	}
	after, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if before != after {
		t.Fatalf("state changed by rejected ops: %+v -> %+v", before, after)
	}
}

// TestConcurrent hammers the analyzer from many goroutines; run with
// -race. The final state must satisfy the global invariants.
func TestConcurrent(t *testing.T) {
	a := exampleAnalyzer(t, 1)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch (g + i) % 4 {
				case 0:
					_ = a.SetPhase("B", i%6)
				case 1:
					_ = a.SetDelay("B", 1+i%6)
				case 2:
					_, _ = a.Analyze("c")
				case 3:
					_, _ = a.Tune("c")
				}
			}
		}(g)
	}
	wg.Wait()
	res, err := a.Analyze("c")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.MaxAge != res.MaxReaction {
		t.Fatalf("MaxAge %d != MaxReaction %d", res.MaxAge, res.MaxReaction)
	}
	if res.MinReaction > res.MaxReaction {
		t.Fatalf("MinReaction %d > MaxReaction %d", res.MinReaction, res.MaxReaction)
	}
}

// TestReplayDeterminism: the same operation sequence replayed on a fresh
// analyzer yields identical results.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]Result, TuneResult) {
		a := NewAnalyzer()
		mustAddTask(t, a, "A", 4, 0, 4)
		mustAddTask(t, a, "B", 6, 1, 6)
		mustAddTask(t, a, "C", 5, 2, 3)
		mustAddChain(t, a, "c", "A", "B", "C")
		var results []Result
		r, _ := a.Analyze("c")
		results = append(results, r)
		if err := a.SetPhase("B", 3); err != nil {
			t.Fatalf("SetPhase: %v", err)
		}
		if err := a.SetDelay("C", 5); err != nil {
			t.Fatalf("SetDelay: %v", err)
		}
		r, _ = a.Analyze("c")
		results = append(results, r)
		tr, err := a.Tune("c")
		if err != nil {
			t.Fatalf("Tune: %v", err)
		}
		r, _ = a.Analyze("c")
		results = append(results, r)
		// Repeated Analyze on unchanged parameters is stable.
		r2, _ := a.Analyze("c")
		results = append(results, r2)
		return results, tr
	}
	r1, tr1 := run()
	r2, tr2 := run()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(tr1, tr2) {
		t.Fatalf("replay mismatch: %v/%+v vs %v/%+v", r1, tr1, r2, tr2)
	}
	if r1[2] != r1[3] {
		t.Fatalf("repeated Analyze differs: %+v vs %+v", r1[2], r1[3])
	}
}
