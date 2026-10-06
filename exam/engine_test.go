package exam

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustCat(t *testing.T, err error, cat Category) {
	t.Helper()
	if err == nil {
		t.Fatalf("want category %s, got nil error", cat)
	}
	var ee *Error
	if !errors.As(err, &ee) {
		t.Fatalf("error is not *exam.Error: %v", err)
	}
	if ee.Cat != cat {
		t.Fatalf("want category %s, got %s (%s)", cat, ee.Cat, ee.Msg)
	}
}

func addQ(t *testing.T, e *Engine, id string, score, diff int, know ...string) {
	t.Helper()
	mustOK(t, e.AddQuestion(id, score, diff, know))
}

// Total score must equal the target exactly: exact passes, off-by-one fails
// with ErrTotalScore and leaves the draft untouched.
func TestPublishTotalScoreExactAndOffByOne(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1)
	addQ(t, e, "q2", 10, 1)
	addQ(t, e, "q3", 9, 1)
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q3"))
	before := e.Digest()
	mustCat(t, e.Publish("p"), ErrTotalScore) // 19 != 20
	if e.Digest() != before {
		t.Fatal("failed publish changed the draft")
	}
	mustOK(t, e.DraftRemove("p", "q3"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p")) // 10 + 10 == 20
	if st, _ := e.PaperStateOf("p"); st != Published {
		t.Fatalf("paper not published: %s", st)
	}
}

// Difficulty ranges are closed intervals: counts exactly at Min and Max
// pass, one outside on either side fails with ErrDifficulty.
func TestDifficultyRangeBoundaries(t *testing.T) {
	newEngine := func() *Engine {
		e := NewEngine()
		for i := 0; i < 4; i++ {
			addQ(t, e, fmt.Sprintf("a%d", i), 5, 1)
		}
		return e
	}
	// count == Min == Max == 2 passes
	e := newEngine()
	mustOK(t, e.CreatePaper("p", 10, nil, map[int]DifficultyRange{1: {Min: 2, Max: 2}}))
	mustOK(t, e.DraftAdd("p", "a0"))
	mustOK(t, e.DraftAdd("p", "a1"))
	mustOK(t, e.Publish("p"))
	// count == Min-1 fails
	e = newEngine()
	mustOK(t, e.CreatePaper("p", 5, nil, map[int]DifficultyRange{1: {Min: 2, Max: 3}}))
	mustOK(t, e.DraftAdd("p", "a0"))
	mustCat(t, e.Publish("p"), ErrDifficulty)
	// count == Max+1 fails
	e = newEngine()
	mustOK(t, e.CreatePaper("p", 15, nil, map[int]DifficultyRange{1: {Min: 1, Max: 2}}))
	for _, id := range []string{"a0", "a1", "a2"} {
		mustOK(t, e.DraftAdd("p", id))
	}
	mustCat(t, e.Publish("p"), ErrDifficulty)
}

// Knowledge coverage: exactly the configured count passes, one less fails.
func TestKnowledgeCoverageExact(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 5, 1, "k1")
	addQ(t, e, "q2", 5, 1, "k1")
	addQ(t, e, "q3", 5, 1, "k2")
	mustOK(t, e.CreatePaper("p", 10, map[string]int{"k1": 2}, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q3"))
	mustCat(t, e.Publish("p"), ErrKnowledgeCoverage) // only q1 covers k1
	mustOK(t, e.DraftRemove("p", "q3"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p")) // exactly 2 cover k1
}

// Mutex is the transitive closure over shared groups: a-b, b-c, c-d across
// three groups makes a and d mutually exclusive; removing the bridging
// membership reopens the combination.
func TestMutexTransitiveClosureThreeGroups(t *testing.T) {
	e := NewEngine()
	for _, id := range []string{"a", "b", "c", "d"} {
		addQ(t, e, id, 5, 1)
	}
	mustOK(t, e.AddToGroup("a", "g1"))
	mustOK(t, e.AddToGroup("b", "g1"))
	mustOK(t, e.AddToGroup("b", "g2"))
	mustOK(t, e.AddToGroup("c", "g2"))
	mustOK(t, e.AddToGroup("c", "g3"))
	mustOK(t, e.AddToGroup("d", "g3"))
	mustOK(t, e.CreatePaper("p", 10, nil, nil))
	mustOK(t, e.DraftAdd("p", "a"))
	mustCat(t, e.DraftAdd("p", "d"), ErrMutexConflict) // closure a-b-c-d
	mustCat(t, e.DraftAdd("p", "c"), ErrMutexConflict)
	// Removing the bridging membership b-g1 splits the component:
	// a becomes isolated and may now coexist with d, while c and d stay
	// connected through g2/g3.
	mustOK(t, e.RemoveFromGroup("b", "g1"))
	mustOK(t, e.DraftAdd("p", "d"))
	mustCat(t, e.DraftAdd("p", "c"), ErrMutexConflict)
}

// Revising a question after publish must not change the paper's frozen
// attributes, and later replacements must validate against the frozen
// values, not the latest ones.
func TestReviseAfterPublishKeepsSnapshot(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1, "k1")
	addQ(t, e, "q2", 10, 2, "k2")
	addQ(t, e, "q3", 10, 2, "k2")
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p"))
	if _, err := e.ReviseQuestion("q1", 99, 3, []string{"k9"}); err != nil {
		t.Fatal(err)
	}
	score, diff, know, ok := e.ItemAttrs("p", "q1")
	if !ok || score != 10 || diff != 1 || len(know) != 1 || know[0] != "k1" {
		t.Fatalf("frozen attrs changed: %d %d %v", score, diff, know)
	}
	if items := e.PaperItems("p"); items["q1"] != 1 {
		t.Fatalf("bound version changed: %v", items)
	}
	// Replacement validates against the frozen 10 points of q1, not 99.
	mustOK(t, e.Replace("p", "q2", "q3"))
}

// A suspended question stays valid inside an already published paper, but
// cannot be selected by new drafts.
func TestSuspendedQuestionStaysInPublishedPaper(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1)
	addQ(t, e, "q2", 10, 1)
	addQ(t, e, "q3", 10, 1)
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p"))
	mustOK(t, e.SuspendQuestion("q1"))
	if st, _ := e.PaperStateOf("p"); st != Published {
		t.Fatalf("suspend changed paper state: %s", st)
	}
	mustOK(t, e.CreatePaper("p2", 10, nil, nil))
	mustCat(t, e.DraftAdd("p2", "q1"), ErrNotSelectable)
	// Replacing the suspended question inside the published paper works.
	mustOK(t, e.Replace("p", "q1", "q3"))
}

// Retiring a question invalidates every published paper containing it and
// reports the list; replacing retired questions repairs the paper, fully
// recovering only once no retired question remains.
func TestRetireInvalidatesAndReplaceRestores(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1, "k1")
	addQ(t, e, "q2", 10, 1, "k1")
	addQ(t, e, "q3", 10, 1, "k1")
	addQ(t, e, "r1", 10, 1, "k1")
	addQ(t, e, "r2", 10, 1, "k1")
	mustOK(t, e.CreatePaper("p", 30, map[string]int{"k1": 3}, nil))
	for _, id := range []string{"q1", "q2", "q3"} {
		mustOK(t, e.DraftAdd("p", id))
	}
	mustOK(t, e.Publish("p"))
	mustOK(t, e.CreatePaper("p2", 10, nil, nil))
	mustOK(t, e.DraftAdd("p2", "q1"))
	mustOK(t, e.Publish("p2"))

	affected, err := e.RetireQuestion("q1")
	mustOK(t, err)
	if len(affected) != 2 || affected[0] != "p" || affected[1] != "p2" {
		t.Fatalf("affected list: %v", affected)
	}
	if st, _ := e.PaperStateOf("p"); st != Invalid {
		t.Fatalf("p should be invalid: %s", st)
	}
	// Replacing a non-retired question of an invalid paper is rejected and
	// distinguishable from other failures.
	mustCat(t, e.Replace("p", "q2", "r1"), ErrInvalidState)
	// Repair p but leave p2 invalid.
	mustOK(t, e.Replace("p", "q1", "r1"))
	if st, _ := e.PaperStateOf("p"); st != Published {
		t.Fatalf("p should recover: %s", st)
	}
	if st, _ := e.PaperStateOf("p2"); st != Invalid {
		t.Fatalf("p2 should stay invalid: %s", st)
	}
	// Retire two more questions of p: replacing only one of the retired
	// questions applies but keeps the paper invalid.
	if _, err := e.RetireQuestion("q2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RetireQuestion("q3"); err != nil {
		t.Fatal(err)
	}
	mustOK(t, e.Replace("p", "q2", "r2"))
	if st, _ := e.PaperStateOf("p"); st != Invalid {
		t.Fatalf("p should stay invalid while q3 is retired: %s", st)
	}
	addQ(t, e, "r3", 10, 1, "k1")
	mustOK(t, e.Replace("p", "q3", "r3"))
	if st, _ := e.PaperStateOf("p"); st != Published {
		t.Fatalf("p should recover after last retired replaced: %s", st)
	}
}

// Group membership changes after publish are not retroactive, but they do
// constrain later replacements.
func TestGroupChangeAfterPublishNotRetroactive(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1)
	addQ(t, e, "q2", 10, 1)
	addQ(t, e, "q3", 10, 1)
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p"))
	// Replace q2 with q3, then make q3 conflict with q1: the published
	// paper keeps its state despite the new conflict.
	mustOK(t, e.Replace("p", "q2", "q3"))
	mustOK(t, e.AddToGroup("q1", "g"))
	mustOK(t, e.AddToGroup("q3", "g"))
	if st, _ := e.PaperStateOf("p"); st != Published {
		t.Fatalf("group change retroactively changed paper: %s", st)
	}
	// But the new relation blocks a later replacement that would add a
	// question conflicting with q1.
	addQ(t, e, "q4", 10, 1)
	mustOK(t, e.AddToGroup("q4", "g"))
	before := e.Digest()
	mustCat(t, e.Replace("p", "q3", "q4"), ErrMutexConflict)
	if e.Digest() != before {
		t.Fatal("failed replace changed the paper")
	}
}

// Illegal lifecycle transitions are rejected with ErrInvalidState and are
// distinguishable from not-found and from each other's messages.
func TestLifecycleTransitions(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q", 5, 1)
	mustCat(t, e.ResumeQuestion("q"), ErrInvalidState) // available -> resume
	mustCat(t, e.SuspendQuestion("nope"), ErrNotFound) // missing question
	mustOK(t, e.SuspendQuestion("q"))
	mustCat(t, e.SuspendQuestion("q"), ErrInvalidState) // suspended -> suspend
	mustOK(t, e.ResumeQuestion("q"))
	mustOK(t, e.SuspendQuestion("q"))
	if _, err := e.RetireQuestion("q"); err != nil { // suspended -> retired
		t.Fatal(err)
	}
	mustCat(t, e.ResumeQuestion("q"), ErrInvalidState) // retired is terminal
	mustCat(t, e.SuspendQuestion("q"), ErrInvalidState)
	if _, err := e.RetireQuestion("q"); true {
		mustCat(t, err, ErrInvalidState)
	}
}

// Rejected operations change nothing and consume no version numbers.
func TestRejectedOpsAreAtomic(t *testing.T) {
	e := NewEngine()
	addQ(t, e, "q1", 10, 1)
	addQ(t, e, "q2", 10, 1)
	addQ(t, e, "q3", 5, 1)
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p"))

	before := e.Digest()
	// failed revise: negative score, version number not consumed
	if _, err := e.ReviseQuestion("q1", -1, 1, nil); true {
		mustCat(t, err, ErrInvalidParam)
	}
	if n := e.VersionCount("q1"); n != 1 {
		t.Fatalf("rejected revise consumed a version: %d", n)
	}
	// failed replace: total score would break
	mustCat(t, e.Replace("p", "q2", "q3"), ErrTotalScore)
	// failed draft op on published paper
	mustCat(t, e.DraftAdd("p", "q3"), ErrInvalidState)
	// failed replace on a draft
	mustOK(t, e.CreatePaper("d", 0, nil, nil))
	mustCat(t, e.Replace("d", "q1", "q3"), ErrNotFound) // q1 not in draft d
	mustOK(t, e.DraftAdd("d", "q1"))
	mustCat(t, e.Replace("d", "q1", "q3"), ErrInvalidState)
	if e.Digest() == before {
		t.Fatal("expected digest to change after successful DraftAdd")
	}
	before = e.Digest()
	mustCat(t, e.Replace("p", "q2", "q3"), ErrTotalScore)
	mustCat(t, e.DraftRemove("p", "q1"), ErrInvalidState)
	if e.Digest() != before {
		t.Fatal("rejected operations changed state")
	}
}

// Each adjacent pair of the priority ladder: when two rules are violated,
// the higher-priority category must be reported.
func TestRejectionPriorityPairs(t *testing.T) {
	// invalid-param > not-found
	e := NewEngine()
	if _, err := e.ReviseQuestion("", -1, 0, nil); true {
		mustCat(t, err, ErrInvalidParam)
	}
	// not-found > invalid-state: removing a question that is not in the
	// (already published) paper reports not-found, not invalid-state.
	addQ(t, e, "q1", 10, 1)
	addQ(t, e, "q2", 10, 1)
	addQ(t, e, "q3", 10, 1)
	mustOK(t, e.CreatePaper("p", 20, nil, nil))
	mustOK(t, e.DraftAdd("p", "q1"))
	mustOK(t, e.DraftAdd("p", "q2"))
	mustOK(t, e.Publish("p"))
	mustCat(t, e.DraftRemove("p", "q3"), ErrNotFound)
	// invalid-state > not-selectable: adding a suspended question to a
	// published paper reports invalid-state.
	mustOK(t, e.SuspendQuestion("q3"))
	mustCat(t, e.DraftAdd("p", "q3"), ErrInvalidState)
	// not-selectable > mutex: adding a suspended question that would also
	// conflict reports not-selectable.
	mustOK(t, e.CreatePaper("d", 0, nil, nil))
	mustOK(t, e.DraftAdd("d", "q1"))
	mustOK(t, e.AddToGroup("q1", "g"))
	mustOK(t, e.AddToGroup("q3", "g"))
	mustCat(t, e.DraftAdd("d", "q3"), ErrNotSelectable)
	// mutex > total-score: draft with a conflict (created after the add)
	// and a wrong total reports mutex first.
	e3 := NewEngine()
	addQ(t, e3, "x", 10, 1)
	addQ(t, e3, "y", 10, 1)
	mustOK(t, e3.CreatePaper("d", 99, nil, nil))
	mustOK(t, e3.DraftAdd("d", "x"))
	mustOK(t, e3.DraftAdd("d", "y"))
	mustOK(t, e3.AddToGroup("x", "g"))
	mustOK(t, e3.AddToGroup("y", "g"))
	mustCat(t, e3.ValidatePaper("d"), ErrMutexConflict)
	// total-score > knowledge: wrong total and missing knowledge point.
	e2 := NewEngine()
	addQ(t, e2, "a", 10, 1, "k1")
	addQ(t, e2, "b", 10, 1)
	mustOK(t, e2.CreatePaper("p", 99, map[string]int{"k2": 1}, nil))
	mustOK(t, e2.DraftAdd("p", "a"))
	mustOK(t, e2.DraftAdd("p", "b"))
	mustCat(t, e2.Publish("p"), ErrTotalScore)
	// knowledge > difficulty: coverage short and difficulty out of range.
	mustOK(t, e2.CreatePaper("p2", 20, map[string]int{"k2": 1}, map[int]DifficultyRange{1: {Min: 5, Max: 9}}))
	mustOK(t, e2.DraftAdd("p2", "a"))
	mustOK(t, e2.DraftAdd("p2", "b"))
	mustCat(t, e2.Publish("p2"), ErrKnowledgeCoverage)
}

// The conflict check against a paper costs O(paper size) map reads and does
// not grow with the total number of questions or groups.
func TestConflictCheckCostIndependentOfTotals(t *testing.T) {
	e := NewEngine()
	rng := rand.New(rand.NewSource(1))
	const questions, groups = 3000, 1500
	for i := 0; i < questions; i++ {
		addQ(t, e, fmt.Sprintf("q%d", i), 5, 1)
	}
	for i := 0; i < questions; i++ {
		// One membership each keeps components small (no giant component),
		// while the bank and group table stay large.
		_ = e.AddToGroup(fmt.Sprintf("q%d", i), fmt.Sprintf("g%d", rng.Intn(groups)))
	}
	mustOK(t, e.CreatePaper("p", 0, nil, nil))
	var inPaper []string
	for attempts := 0; len(inPaper) < 5 && attempts < 1000; attempts++ {
		id := fmt.Sprintf("q%d", rng.Intn(questions))
		if err := e.DraftAdd("p", id); err == nil {
			inPaper = append(inPaper, id)
		}
	}
	if len(inPaper) < 5 {
		t.Fatal("could not assemble a conflict-free paper")
	}
	// Correctness spot check against a brute-force BFS over the raw graph.
	connected := func(a, b string) bool {
		seen := map[string]bool{a: true}
		queue := []string{a}
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			if x == b {
				return true
			}
			for g := range e.qgroups[x] {
				for y := range e.groups[g] {
					if !seen[y] {
						seen[y] = true
						queue = append(queue, y)
					}
				}
			}
		}
		return false
	}
	for i := 0; i < 50; i++ {
		cand := fmt.Sprintf("q%d", rng.Intn(questions))
		want := false
		for _, id := range inPaper {
			if connected(cand, id) {
				want = true
				break
			}
		}
		e.ResetCheckOps()
		if got := e.Conflicts("p", cand); got != want {
			t.Fatalf("Conflicts(%s) = %v, want %v", cand, got, want)
		}
		if ops := e.CheckOps(); ops > len(inPaper) {
			t.Fatalf("conflict check used %d reads with %d questions / %d groups; want <= %d",
				ops, questions, groups, len(inPaper))
		}
	}
}

// Concurrent callers: results must be race-free and equivalent to some
// serial order (the engine serializes internally).
func TestConcurrentAccess(t *testing.T) {
	e := NewEngine()
	for i := 0; i < 20; i++ {
		addQ(t, e, fmt.Sprintf("q%d", i), 5, 1)
	}
	mustOK(t, e.CreatePaper("p", 25, nil, nil))
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				id := fmt.Sprintf("q%d", rng.Intn(20))
				switch rng.Intn(7) {
				case 0:
					_ = e.DraftAdd("p", id)
				case 1:
					_ = e.DraftRemove("p", id)
				case 2:
					_, _ = e.ReviseQuestion(id, rng.Intn(10), rng.Intn(3), nil)
				case 3:
					_ = e.SuspendQuestion(id)
				case 4:
					_ = e.ResumeQuestion(id)
				case 5:
					_ = e.AddToGroup(id, fmt.Sprintf("g%d", rng.Intn(4)))
				case 6:
					_ = e.ValidatePaper("p")
				}
			}
		}(int64(w))
	}
	wg.Wait()
}
