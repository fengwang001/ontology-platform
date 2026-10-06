package enrollment

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func mustEnroll(t *testing.T, e *Engine, s StudentID, picks ...Pick) {
	t.Helper()
	if err := e.BatchEnroll(s, picks); err != nil {
		t.Fatalf("BatchEnroll(%s, %v) unexpectedly failed: %v", s, picks, err)
	}
}

func wantErr(t *testing.T, err *Error, code Code) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want rejection %s, got success", code)
	}
	if err.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return err
}

func TestPrereqPassLineEquality(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 3)
	e.AddCourse("B", 3)
	e.AddCourse("C0", 3)
	e.AddSection("SA", "A", 3, 2)
	e.AddSection("SB", "B", 3, 1)
	e.AddSection("SC0", "C0", 3, 3)
	e.AddPrereq("A", "B")
	e.AddPrereq("B", "C0") // chain A -> B -> C0

	// Grade exactly at the pass line counts as passed.
	e.SetRecord("s", "B", 60)
	mustEnroll(t, e, "s", Pick{"A", "SA"})

	// One point below the line does not.
	e.SetRecord("s2", "B", 59)
	wantErr(t, e.BatchEnroll("s2", []Pick{{"A", "SA"}}), CodePrereqUnmet)

	// Chains are not traced: only direct prerequisites are judged, so
	// s3 (passed B, never took C0) may take A but not B.
	e.SetRecord("s3", "B", 60)
	mustEnroll(t, e, "s3", Pick{"A", "SA"})
	wantErr(t, e.BatchEnroll("s3", []Pick{{"B", "SB"}}), CodePrereqUnmet)
}

func TestCreditLimitEquality(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 10})
	e.AddCourse("X", 6)
	e.AddCourse("Y", 4)
	e.AddCourse("Z", 1)
	e.AddSection("SX", "X", 5, 1)
	e.AddSection("SY", "Y", 5, 2)
	e.AddSection("SZ", "Z", 5, 3)

	// 6 + 4 == 10 is allowed.
	mustEnroll(t, e, "s", Pick{"X", "SX"}, Pick{"Y", "SY"})
	// 10 + 1 > 10 is rejected.
	wantErr(t, e.BatchEnroll("s", []Pick{{"Z", "SZ"}}), CodeCreditExceeded)
}

func TestCoreqBatchOrder(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 3)
	e.AddCourse("B", 3)
	e.AddSection("SA", "A", 10, 1)
	e.AddSection("SB", "B", 10, 2)
	e.AddCoreq("A", "B")

	// Both orders inside one batch satisfy the coreq relation.
	mustEnroll(t, e, "s1", Pick{"A", "SA"}, Pick{"B", "SB"})
	mustEnroll(t, e, "s2", Pick{"B", "SB"}, Pick{"A", "SA"})

	// Alone, neither is selectable.
	wantErr(t, e.BatchEnroll("s3", []Pick{{"A", "SA"}}), CodeCoreqUnmet)
	wantErr(t, e.BatchEnroll("s4", []Pick{{"B", "SB"}}), CodeCoreqUnmet)

	// A coreq already selected this term supports a later batch.
	mustEnroll(t, e, "s5", Pick{"A", "SA"}, Pick{"B", "SB"})
	if err := e.Drop("s5", "B"); err != nil {
		t.Fatalf("drop B: %v", err)
	}
	// A was cascaded away too; re-enrolling A alone still fails.
	wantErr(t, e.BatchEnroll("s5", []Pick{{"A", "SA"}}), CodeCoreqUnmet)
}

func TestDropCascadeTwoLevels(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 2)
	e.AddCourse("B", 2)
	e.AddCourse("C", 2)
	e.AddSection("SA", "A", 5, 1)
	e.AddSection("SB", "B", 5, 2)
	e.AddSection("SC", "C", 5, 3)
	e.AddCoreq("A", "B")
	e.AddCoreq("B", "C")

	mustEnroll(t, e, "s", Pick{"A", "SA"}, Pick{"B", "SB"}, Pick{"C", "SC"})
	if err := e.Drop("s", "A"); err != nil {
		t.Fatalf("drop A: %v", err)
	}
	// Dropping A strands B (lost coreq A), and dropping B strands C:
	// the cascade chains two levels and releases all seats.
	if got := e.Enrolled("s"); len(got) != 0 {
		t.Fatalf("want empty selection after cascade, got %v", got)
	}
	for _, sec := range []SectionID{"SA", "SB", "SC"} {
		if got := e.Count(sec); got != 0 {
			t.Fatalf("section %s: want 0 enrolled, got %d", sec, got)
		}
	}
}

func TestDropNotEnrolledDistinct(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 2)
	e.AddSection("SA", "A", 5, 1)

	err := e.Drop("s", "A")
	wantErr(t, err, CodeNotEnrolled)
	// Distinguishable from a missing course.
	wantErr(t, e.Drop("s", "ghost"), CodeNotFound)
	if CodeNotEnrolled == CodeNotFound {
		t.Fatal("not_enrolled and not_found must be distinct codes")
	}
	_ = err
}

func TestCascadeReleaseCapacitySequential(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 2)
	e.AddCourse("B", 2)
	e.AddSection("SA", "A", 5, 1)
	e.AddSection("SB", "B", 1, 2) // single seat
	e.AddCoreq("A", "B")

	mustEnroll(t, e, "s1", Pick{"A", "SA"}, Pick{"B", "SB"})
	// s2 cannot get the only seat of SB.
	wantErr(t, e.BatchEnroll("s2", []Pick{{"A", "SA"}, {"B", "SB"}}), CodeCapacityFull)

	// Dropping A cascades to B and frees the seat; s2 takes it.
	if err := e.Drop("s1", "A"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	mustEnroll(t, e, "s2", Pick{"A", "SA"}, Pick{"B", "SB"})
	if got := e.Count("SB"); got != 1 {
		t.Fatalf("want SB count 1, got %d", got)
	}
}

func TestCascadeReleaseCapacityConcurrent(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 2)
	e.AddCourse("B", 2)
	e.AddSection("SA", "A", 5, 1)
	e.AddSection("SB", "B", 1, 2)
	e.AddCoreq("A", "B")
	mustEnroll(t, e, "s1", Pick{"A", "SA"}, Pick{"B", "SB"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := e.Drop("s1", "A"); err != nil {
			t.Errorf("drop: %v", err)
		}
	}()
	grabbed := make(chan bool, 1)
	go func() {
		defer wg.Done()
		// Retry until the cascaded seat becomes visible; must succeed
		// well before the attempt budget runs out.
		for i := 0; i < 10000; i++ {
			if err := e.BatchEnroll("s2", []Pick{{"A", "SA"}, {"B", "SB"}}); err == nil {
				grabbed <- true
				return
			}
		}
		grabbed <- false
	}()
	wg.Wait()
	if !<-grabbed {
		t.Fatal("s2 never grabbed the cascaded seat")
	}
	if got := e.Count("SB"); got != 1 {
		t.Fatalf("capacity invariant violated: SB count %d > 1", got)
	}
	if got := len(e.Enrolled("s1")); got != 0 {
		t.Fatalf("s1 should hold nothing after cascade, got %d courses", got)
	}
}

func TestCapacityDownAdjust(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("K", 2)
	e.AddSection("SK", "K", 2, 1)
	mustEnroll(t, e, "s1", Pick{"K", "SK"})
	mustEnroll(t, e, "s2", Pick{"K", "SK"})

	// Lower below the current count: nobody is kicked out, but no new
	// pick is accepted.
	if err := e.SetCapacity("SK", 1); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	if got := len(e.Enrolled("s1")); got != 1 {
		t.Fatalf("down-adjust must not kick students, s1 holds %d courses", got)
	}
	wantErr(t, e.BatchEnroll("s3", []Pick{{"K", "SK"}}), CodeCapacityFull)

	// One drop: count 1 still equals capacity 1, still rejected.
	if err := e.Drop("s1", "K"); err != nil {
		t.Fatalf("drop s1: %v", err)
	}
	wantErr(t, e.BatchEnroll("s3", []Pick{{"K", "SK"}}), CodeCapacityFull)

	// Second drop brings the count below capacity: enrollment resumes.
	if err := e.Drop("s2", "K"); err != nil {
		t.Fatalf("drop s2: %v", err)
	}
	mustEnroll(t, e, "s3", Pick{"K", "SK"})

	wantErr(t, e.SetCapacity("SK", -1), CodeInvalidParam)
	wantErr(t, e.SetCapacity("ghost", 1), CodeNotFound)
}

func switchFixture() *Engine {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("W", 2)
	e.AddCourse("X", 2)
	e.AddCourse("Y", 2)
	e.AddCourse("Q", 2)
	e.AddCourse("Z", 2)
	e.AddSection("SW", "W", 5, 1)
	e.AddSection("SW2", "W", 5, 4)
	e.AddSection("SX", "X", 5, 2)
	e.AddSection("SY", "Y", 5, 3)
	e.AddSection("SQ", "Q", 5, 5)
	e.AddSection("SZ", "Z", 5, 6)
	e.AddMutex("X", "Y")
	return e
}

func TestSwitchToMutexCourse(t *testing.T) {
	e := switchFixture()
	mustEnroll(t, e, "s", Pick{"W", "SW"}, Pick{"X", "SX"})

	// Switching W -> Y conflicts with the currently selected X.
	wantErr(t, e.Switch("s", "SW", "SY"), CodeMutexConflict)
	// The old selection is fully preserved.
	if got := e.Enrolled("s"); got["W"] != "SW" || got["X"] != "SX" || len(got) != 2 {
		t.Fatalf("old selection not preserved: %v", got)
	}
	if e.Count("SW") != 1 || e.Count("SY") != 0 {
		t.Fatalf("ledger changed on failed switch: SW=%d SY=%d", e.Count("SW"), e.Count("SY"))
	}

	// Mutex with a past-passed course also blocks the switch.
	e.SetRecord("s2", "X", 90)
	mustEnroll(t, e, "s2", Pick{"W", "SW"})
	wantErr(t, e.Switch("s2", "SW", "SY"), CodeMutexConflict)
	if got := e.Enrolled("s2"); got["W"] != "SW" || len(got) != 1 {
		t.Fatalf("old selection not preserved: %v", got)
	}
}

func TestSwitchSameSectionParam(t *testing.T) {
	e := switchFixture()
	mustEnroll(t, e, "s", Pick{"W", "SW"})
	wantErr(t, e.Switch("s", "SW", "SW"), CodeInvalidParam)
	// Even when the section does not exist, param beats not-found.
	wantErr(t, e.Switch("s", "ghost", "ghost"), CodeInvalidParam)
}

func TestSwitchSuccessSameCourse(t *testing.T) {
	e := switchFixture()
	mustEnroll(t, e, "s", Pick{"W", "SW"})
	if err := e.Switch("s", "SW", "SW2"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := e.Enrolled("s"); got["W"] != "SW2" || len(got) != 1 {
		t.Fatalf("want W->SW2, got %v", got)
	}
	if e.Count("SW") != 0 || e.Count("SW2") != 1 {
		t.Fatalf("ledger not moved: SW=%d SW2=%d", e.Count("SW"), e.Count("SW2"))
	}
}

func TestSwitchFullSectionRetainsOld(t *testing.T) {
	e := switchFixture()
	if err := e.SetCapacity("SQ", 0); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	mustEnroll(t, e, "s", Pick{"W", "SW"})
	wantErr(t, e.Switch("s", "SW", "SQ"), CodeCapacityFull)
	if got := e.Enrolled("s"); got["W"] != "SW" || len(got) != 1 {
		t.Fatalf("old selection not preserved: %v", got)
	}
}

func TestSwitchBreakingCoreqRejected(t *testing.T) {
	e := switchFixture()
	e.AddCoreq("W", "Z")
	mustEnroll(t, e, "s", Pick{"W", "SW"}, Pick{"Z", "SZ"})
	// Switching W away would strand Z's coreq support: rejected.
	wantErr(t, e.Switch("s", "SW", "SQ"), CodeCoreqUnmet)
	if got := e.Enrolled("s"); got["W"] != "SW" || got["Z"] != "SZ" || len(got) != 2 {
		t.Fatalf("old selection not preserved: %v", got)
	}
	// Switching within the same course keeps the coreq support: allowed.
	if err := e.Switch("s", "SW", "SW2"); err != nil {
		t.Fatalf("same-course switch should keep coreq support: %v", err)
	}
}

func TestSwitchNotEnrolled(t *testing.T) {
	e := switchFixture()
	wantErr(t, e.Switch("s", "SW", "SQ"), CodeNotEnrolled)
	wantErr(t, e.Switch("s", "ghost", "SQ"), CodeNotFound)
}

func TestBatchFirstFailureAttribution(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("OK", 1)
	e.AddCourse("TC", 1)
	e.AddCourse("CF", 1)
	e.AddSection("SOK", "OK", 5, 1)
	e.AddSection("STC", "TC", 5, 1) // clashes with SOK inside the batch
	e.AddSection("SCF", "CF", 0, 3) // full (capacity 0)

	// The first failing pick in submission order is reported.
	err := e.BatchEnroll("s", []Pick{{"OK", "SOK"}, {"TC", "STC"}, {"CF", "SCF"}})
	wantErr(t, err, CodeTimeConflict)
	if err.Course != "TC" {
		t.Fatalf("want failing course TC, got %q", err.Course)
	}

	// Reorder: now the capacity failure comes first.
	err = e.BatchEnroll("s", []Pick{{"CF", "SCF"}, {"OK", "SOK"}, {"TC", "STC"}})
	wantErr(t, err, CodeCapacityFull)
	if err.Course != "CF" {
		t.Fatalf("want failing course CF, got %q", err.Course)
	}
}

func TestBatchAllOrNothing(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("A", 2)
	e.AddCourse("B", 2)
	e.AddSection("SA", "A", 5, 1)
	e.AddSection("SB", "B", 0, 2) // full

	wantErr(t, e.BatchEnroll("s", []Pick{{"A", "SA"}, {"B", "SB"}}), CodeCapacityFull)
	if got := e.Enrolled("s"); len(got) != 0 {
		t.Fatalf("rejected batch must change nothing, got %v", got)
	}
	if got := e.Count("SA"); got != 0 {
		t.Fatalf("rejected batch must not take seats, SA=%d", got)
	}

	// Duplicated course in one batch is a param error.
	wantErr(t, e.BatchEnroll("s", []Pick{{"A", "SA"}, {"A", "SA"}}), CodeInvalidParam)
	// Empty batch is a param error.
	wantErr(t, e.BatchEnroll("s", nil), CodeInvalidParam)
	// Section not belonging to the named course is a param error.
	wantErr(t, e.BatchEnroll("s", []Pick{{"A", "SB"}}), CodeInvalidParam)
}

func TestTimeConflict(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("P", 2)
	e.AddCourse("Q", 2)
	e.AddCourse("R", 2)
	e.AddSection("SP", "P", 5, 1, 2)
	e.AddSection("SQ", "Q", 5, 2, 3)
	e.AddSection("SR", "R", 5, 3)

	mustEnroll(t, e, "s", Pick{"P", "SP"})
	wantErr(t, e.BatchEnroll("s", []Pick{{"Q", "SQ"}}), CodeTimeConflict) // shares slot 2
	mustEnroll(t, e, "s", Pick{"R", "SR"})                                // disjoint
}

// violScenario builds an engine in which the candidate pick exhibits
// exactly the given violations, and returns the batch to submit.
// Credit math: any pre-enrollment totals exactly 10 credits (the
// ceiling) when CodeCreditExceeded is requested, and stays well below
// it otherwise, so no unintended violation sneaks in.
func violScenario(t *testing.T, viol ...Code) (*Engine, StudentID, []Pick) {
	t.Helper()
	has := func(c Code) bool {
		for _, v := range viol {
			if v == c {
				return true
			}
		}
		return false
	}
	e := NewEngine(Config{PassLine: 60, MaxCredits: 10})
	for c, cr := range map[CourseID]int{
		"C": 1, "W1": 5, "W2": 4, "W3": 1, "M": 1, "T": 1, "P": 1, "Co": 1,
	} {
		e.AddCourse(c, cr)
	}
	e.AddSection("SecC", "C", 1, 9)
	e.AddSection("SecC0", "C", 1, 0)
	e.AddSection("SecC2", "C", 1, 8)
	e.AddSection("SecW1", "W1", 1, 2)
	e.AddSection("SecW2", "W2", 1, 3)
	e.AddSection("SecW3", "W3", 1, 4)
	e.AddSection("SecM", "M", 1, 1)
	e.AddSection("SecT", "T", 1, 9)
	e.AddSection("SecP", "P", 1, 5)
	e.AddSection("SecCo", "Co", 1, 6)
	stu := StudentID("s")

	if has(CodeCapacityFull) {
		mustEnroll(t, e, "other", Pick{"C", "SecC"}) // fills the only seat
	}
	var pre []Pick
	if has(CodeAlreadyEnrolled) {
		pre = append(pre, Pick{"C", "SecC0"})
	}
	if has(CodeMutexConflict) {
		pre = append(pre, Pick{"M", "SecM"})
	}
	if has(CodeCreditExceeded) {
		pre = append(pre, Pick{"W1", "SecW1"}, Pick{"W2", "SecW2"})
		if !has(CodeAlreadyEnrolled) && !has(CodeMutexConflict) && !has(CodeTimeConflict) {
			pre = append(pre, Pick{"W3", "SecW3"})
		}
	}
	if has(CodeTimeConflict) {
		pre = append(pre, Pick{"T", "SecT"})
	}
	if len(pre) > 0 {
		mustEnroll(t, e, stu, pre...)
	}
	// Relations are added after the setup enrollments so they cannot
	// block them.
	if has(CodeMutexConflict) {
		e.AddMutex("M", "C")
	}
	if has(CodePrereqUnmet) {
		e.AddPrereq("C", "P")
	}
	if has(CodeCoreqUnmet) {
		e.AddCoreq("C", "Co")
	}

	pick := Pick{Course: "C", Section: "SecC"}
	if has(CodeNotFound) {
		if has(CodeTimeConflict) || has(CodeCapacityFull) {
			// Missing course; the existing section still carries the
			// time/capacity violation condition.
			pick = Pick{Course: "Cghost", Section: "SecC"}
		} else {
			// Missing section; the existing course still carries the
			// enrolled/mutex/prereq/coreq/credit violation condition.
			pick = Pick{Course: "C", Section: "SecMissing"}
		}
	}
	picks := []Pick{pick}
	if has(CodeInvalidParam) {
		picks = append(picks, Pick{"C", "SecC2"}) // duplicate course in batch
	}
	return e, stu, picks
}

func TestRejectPriorityPairwise(t *testing.T) {
	order := []Code{
		CodeInvalidParam, CodeNotFound, CodeAlreadyEnrolled, CodeMutexConflict,
		CodePrereqUnmet, CodeCoreqUnmet, CodeCreditExceeded, CodeTimeConflict,
		CodeCapacityFull,
	}
	// Each violation alone is reported as itself.
	for _, c := range order {
		e, stu, picks := violScenario(t, c)
		wantErr(t, e.BatchEnroll(stu, picks), c)
	}
	// For every pair, the higher-priority violation is reported.
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			hi, lo := order[i], order[j]
			t.Run(fmt.Sprintf("%s_over_%s", hi, lo), func(t *testing.T) {
				e, stu, picks := violScenario(t, hi, lo)
				wantErr(t, e.BatchEnroll(stu, picks), hi)
			})
		}
	}
}

// buildScaleEngine creates a universe of n courses (one section each),
// enrolls the student in 5 of them, and returns a 3-slot candidate pick
// that passes every check.
func buildScaleEngine(n int) (*Engine, StudentID, Pick) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 1 << 30})
	for i := 0; i < n; i++ {
		c := CourseID(fmt.Sprintf("c%05d", i))
		e.AddCourse(c, 1)
		e.AddSection(SectionID("sec-"+string(c)), c, n+10, Slot(1000+i))
	}
	e.AddCourse("cand", 1)
	e.AddSection("sec-cand", "cand", n+10, 1, 2, 3)
	stu := StudentID("s")
	var picks []Pick
	for i := 0; i < 5; i++ {
		c := CourseID(fmt.Sprintf("c%05d", i))
		picks = append(picks, Pick{c, SectionID("sec-" + string(c))})
	}
	if err := e.BatchEnroll(stu, picks); err != nil {
		panic(err)
	}
	return e, stu, Pick{"cand", "sec-cand"}
}

// TestComplexityProbes verifies, via probe counters, that a
// time-conflict check costs exactly one map lookup per candidate slot
// and a capacity check exactly one ledger read, independent of the
// universe size and of the section's enrollment count.
func TestComplexityProbes(t *testing.T) {
	probe := func(e *Engine, s StudentID, p Pick) Stats {
		before := e.Stats()
		if err := e.BatchEnroll(s, []Pick{p}); err != nil {
			t.Fatalf("probe pick should pass: %v", err)
		}
		after := e.Stats()
		return Stats{
			SlotProbes:     after.SlotProbes - before.SlotProbes,
			CapacityProbes: after.CapacityProbes - before.CapacityProbes,
		}
	}

	small, ss, sp := buildScaleEngine(10)
	large, ls, lp := buildScaleEngine(5000)
	ds := probe(small, ss, sp)
	dl := probe(large, ls, lp)
	if ds != dl {
		t.Fatalf("probe count grows with universe size: small=%+v large=%+v", ds, dl)
	}
	if ds.SlotProbes != 3 {
		t.Fatalf("want 3 slot probes (one per candidate slot), got %d", ds.SlotProbes)
	}
	if ds.CapacityProbes != 1 {
		t.Fatalf("want 1 capacity probe, got %d", ds.CapacityProbes)
	}

	// Capacity cost does not grow with the section's enrollment count.
	e := NewEngine(Config{PassLine: 60, MaxCredits: 1 << 30})
	e.AddCourse("K", 1)
	e.AddSection("SK", "K", 20000, 9)
	for i := 0; i < 10000; i++ {
		mustEnroll(t, e, StudentID(fmt.Sprintf("u%d", i)), Pick{"K", "SK"})
	}
	d := probe(e, "last", Pick{"K", "SK"})
	if d.CapacityProbes != 1 {
		t.Fatalf("capacity check over %d enrolled students used %d probes", 10000, d.CapacityProbes)
	}
	if d.SlotProbes != 1 {
		t.Fatalf("want 1 slot probe for 1-slot candidate, got %d", d.SlotProbes)
	}
}

func TestConcurrentCapacityInvariant(t *testing.T) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 30})
	e.AddCourse("K", 2)
	e.AddSection("SK", "K", 10, 1)

	var wg sync.WaitGroup
	var okCount atomic.Int64
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := e.BatchEnroll(StudentID(fmt.Sprintf("g%d", i)), []Pick{{"K", "SK"}})
			if err == nil {
				okCount.Add(1)
			} else if err.Code != CodeCapacityFull {
				t.Errorf("unexpected rejection: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := okCount.Load(); got != 10 {
		t.Fatalf("want exactly 10 successes, got %d", got)
	}
	if got := e.Count("SK"); got != 10 {
		t.Fatalf("ledger %d exceeds capacity 10", got)
	}
}

func BenchmarkTimeConflictCheck(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("courses=%d", n), func(b *testing.B) {
			e := NewEngine(Config{PassLine: 60, MaxCredits: 1 << 30})
			for i := 0; i < n; i++ {
				c := CourseID(fmt.Sprintf("c%05d", i))
				e.AddCourse(c, 1)
				e.AddSection(SectionID("sec-"+string(c)), c, 1, Slot(1000+i))
			}
			e.AddCourse("cand", 1)
			e.AddSection("sec-cand", "cand", 1, 5)
			var picks []Pick
			for i := 0; i < 5; i++ {
				c := CourseID(fmt.Sprintf("c%05d", i))
				picks = append(picks, Pick{c, SectionID("sec-" + string(c))})
			}
			if err := e.BatchEnroll("s", picks); err != nil {
				b.Fatal(err)
			}
			// c0000 occupies slot 1000; give the candidate slot 1000 so
			// every call fails with a time conflict after exactly one
			// probe, leaving state untouched for the next iteration.
			e.AddCourse("cand2", 1)
			e.AddSection("sec-cand2", "cand2", 1, 1000)
			pick := []Pick{{"cand2", "sec-cand2"}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := e.BatchEnroll("s", pick); err == nil || err.Code != CodeTimeConflict {
					b.Fatalf("unexpected result: %v", err)
				}
			}
		})
	}
}

func BenchmarkCapacityCheck(b *testing.B) {
	e := NewEngine(Config{PassLine: 60, MaxCredits: 1 << 30})
	e.AddCourse("K", 1)
	e.AddSection("SK", "K", 100000, 9)
	for i := 0; i < 100000; i++ {
		if err := e.BatchEnroll(StudentID(fmt.Sprintf("u%d", i)), []Pick{{"K", "SK"}}); err != nil {
			b.Fatal(err)
		}
	}
	pick := []Pick{{"K", "SK"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.BatchEnroll("ub", pick); err == nil || err.Code != CodeCapacityFull {
			b.Fatalf("unexpected result: %v", err)
		}
	}
}
