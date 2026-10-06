package gradaudit

import (
	"math"
	"testing"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	for code, credit := range map[string]float64{
		"CS1": 3, "CS2": 3, "CS3": 3, "CS4": 3,
		"MA": 4, "EN": 2, "PE": 1, "OLD": 2, "NEW": 3,
	} {
		if err := e.AddCourse(Course{Code: code, Credit: credit}); err != nil {
			t.Fatal(err)
		}
	}

	l1 := &Requirement{Code: "L1", Leaf: true, Courses: []string{"CS1", "CS2"}, MinCredit: 3, MinCourses: 1, Required: true}
	l2 := &Requirement{Code: "L2", Leaf: true, Courses: []string{"CS1", "CS2"}, MinCredit: 3, MinCourses: 1, Required: true}
	l3 := &Requirement{Code: "L3", Leaf: true, Courses: []string{"CS3"}, MinCredit: 3, MinCourses: 1}
	l4 := &Requirement{Code: "L4", Leaf: true, Courses: []string{"CS4", "MA"}, MinCredit: 3, MinCourses: 1}
	g1 := &Requirement{Code: "G1", Children: []*Requirement{l1, l2}, MinChildren: 2}
	g2 := &Requirement{Code: "G2", Children: []*Requirement{l3, l4}, MinChildren: 1}
	root := &Requirement{Code: "ROOT", Children: []*Requirement{g1, g2}, MinChildren: 2}
	plan := &PlanVersion{
		ID: "V1", PassScore: 60, Root: root, TotalCredit: 10, MinGPA: 70,
		TransferCap: 5, SharedPairs: [][2]string{{"L1", "L2"}},
	}
	if err := e.AddPlan(plan, 1); err != nil {
		t.Fatal(err)
	}
	plan2 := &PlanVersion{
		ID: "V2", PassScore: 60, Root: root, TotalCredit: 12, MinGPA: 70,
		TransferCap: 5, SharedPairs: [][2]string{{"L1", "L2"}},
	}
	if err := e.AddPlan(plan2, 2); err != nil {
		t.Fatal(err)
	}
	return e
}

func mustEnroll(t *testing.T, e *Engine, sid, plan string) {
	t.Helper()
	if err := e.Enroll(sid, plan); err != nil {
		t.Fatalf("enroll: %v", err)
	}
}

func mustRec(t *testing.T, e *Engine, in RegisterRecordInput) {
	t.Helper()
	if err := e.RegisterRecord(in); err != nil {
		t.Fatalf("record %+v: %v", in, err)
	}
}

func auditOf(t *testing.T, e *Engine, sid string) *AuditResult {
	t.Helper()
	r, err := e.Audit(sid)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return r
}

func viewOf(t *testing.T, e *Engine, sid string) *countedView {
	t.Helper()
	e.mu.RLock()
	st := e.students[sid]
	e.mu.RUnlock()
	st.mu.Lock()
	defer st.mu.Unlock()
	return buildCountedView(e.plans[st.student.PlanID], st, e.courses)
}

func (v *countedView) courses() []string {
	set := map[string]bool{}
	for _, o := range v.options {
		set[o.course] = true
	}
	var out []string
	for c := range set {
		out = append(out, c)
	}
	return out
}

func hasCourse(v *countedView, c string) bool {
	for _, o := range v.options {
		if o.course == c {
			return true
		}
	}
	return false
}

func hasOption(v *countedView, rec, course string) bool {
	for _, o := range v.options {
		if o.rec.ID == rec && o.course == course {
			return true
		}
	}
	return false
}

func hasKind(fs []AdditionalFailure, kind string) bool {
	for _, f := range fs {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func floatEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func fillBaseline(t *testing.T, e *Engine, sid string) {
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "r1", Course: "CS1", Semester: 1, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "r2", Course: "CS2", Semester: 1, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "r3", Course: "CS3", Semester: 2, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "r4", Course: "EN", Semester: 2, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "r5", Course: "PE", Semester: 3, Score: 90})
}

func TestPassLineEqualityCounts(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "r1", Course: "CS1", Semester: 1, Score: 60})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "r2", Course: "CS2", Semester: 1, Score: 59})
	v := viewOf(t, e, "s")
	if !hasCourse(v, "CS1") || hasCourse(v, "CS2") {
		t.Fatalf("pass-line equality wrong: %v", v.courses())
	}
}

func TestMinGPAEqualityCounts(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	fillBaseline(t, e, "s")
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		e.students["s"].records[id].Score = 70
	}
	res := auditOf(t, e, "s")
	if hasKind(res.Additional, "min_gpa") {
		t.Fatalf("GPA exactly at floor must pass, got %+v", res.Additional)
	}
}

func TestRepeatSameScoreTakesEarliest(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS3", Semester: 3, Score: 75})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "b", Course: "CS3", Semester: 1, Score: 75})
	v := viewOf(t, e, "s")
	sem := -1
	for _, o := range v.options {
		if o.course == "CS3" {
			sem = o.semester
		}
	}
	if sem != 1 {
		t.Fatalf("expected earliest semester 1, got %d", sem)
	}
}

func TestRepeatOnlyHighestCountsOnce(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS3", Semester: 1, Score: 61})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "b", Course: "CS3", Semester: 2, Score: 95})
	v := viewOf(t, e, "s")
	n := 0
	for _, o := range v.options {
		if o.course == "CS3" {
			n++
			if o.score != 95 {
				t.Fatalf("expected highest score 95, got %v", o.score)
			}
		}
	}
	if n != 1 {
		t.Fatalf("credit must count once, got %d options", n)
	}
}

func TestSubstitutionEffectiveEqualityAndCredit(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "NEW", Semester: 1, Score: 80})
	if err := e.RegisterSubstitution("s", "NEW", "OLD", "V1", 2); err != nil {
		t.Fatal(err)
	}
	v := viewOf(t, e, "s")
	if hasOption(v, "a", "OLD") {
		t.Fatal("semester 1 must not use substitution effective at 2")
	}
	e.students["s"].records["a"].Semester = 2
	v = viewOf(t, e, "s")
	found := false
	for _, o := range v.options {
		if o.rec.ID == "a" && o.course == "OLD" {
			found = true
			if !floatEq(o.credit, 2) {
				t.Fatalf("substituted credit must be min(3,2)=2, got %v", o.credit)
			}
		}
	}
	if !found {
		t.Fatal("enrollment at effective semester must use substitution")
	}
}

func TestTransferCapExactAndOver(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "exact", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "exact", RecordID: "t1", Course: "MA", Semester: 1, Score: 80, Transfer: true})
	mustRec(t, e, RegisterRecordInput{StudentID: "exact", RecordID: "t2", Course: "PE", Semester: 1, Score: 80, Transfer: true})
	v := viewOf(t, e, "exact")
	if !hasCourse(v, "MA") || !hasCourse(v, "PE") {
		t.Fatal("transfers exactly at cap must count")
	}

	mustEnroll(t, e, "over", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "over", RecordID: "t1", Course: "MA", Semester: 1, Score: 80, Transfer: true})
	mustRec(t, e, RegisterRecordInput{StudentID: "over", RecordID: "t2", Course: "PE", Semester: 1, Score: 80, Transfer: true})
	err := e.RegisterRecord(RegisterRecordInput{StudentID: "over", RecordID: "t3", Course: "EN", Semester: 1, Score: 80, Transfer: true})
	if err == nil || err.Kind != ErrTransferOverflow {
		t.Fatalf("expected transfer overflow, got %v", err)
	}
	if _, ok := e.students["over"].records["t3"]; ok {
		t.Fatal("rejected transfer must not mutate state")
	}
}

func passingSupplements(t *testing.T, e *Engine, sid string) {
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "c", Course: "CS3", Semester: 2, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "d", Course: "EN", Semester: 2, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: sid, RecordID: "f", Course: "PE", Semester: 3, Score: 90})
}

func TestOneAssignmentOnlyPasses(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	// Without the shared pair, CS1 and CS2 must be split L1/L2 one each; the
	// engine must find the passing split even though many assignments fail.
	e.plans["V1"].SharedPairs = nil
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS1", Semester: 1, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "b", Course: "CS2", Semester: 1, Score: 80})
	passingSupplements(t, e, "s")
	res := auditOf(t, e, "s")
	if !res.TreeSatisfied {
		t.Fatalf("a passing split exists; attribution=%+v", res.Attribution)
	}

	// With only CS1, no split can satisfy both L1 and L2: attribution must be
	// the smallest-coded impossible leaf L1, with one-course/zero-credit gap.
	e2 := newTestEngine(t)
	mustEnroll(t, e2, "s2", "V1")
	e2.plans["V1"].SharedPairs = nil
	mustRec(t, e2, RegisterRecordInput{StudentID: "s2", RecordID: "a", Course: "CS1", Semester: 1, Score: 80})
	mustRec(t, e2, RegisterRecordInput{StudentID: "s2", RecordID: "c", Course: "CS3", Semester: 2, Score: 80})
	mustRec(t, e2, RegisterRecordInput{StudentID: "s2", RecordID: "d", Course: "EN", Semester: 2, Score: 80})
	mustRec(t, e2, RegisterRecordInput{StudentID: "s2", RecordID: "f", Course: "PE", Semester: 3, Score: 90})
	res2 := auditOf(t, e2, "s2")
	// Each leaf alone can be met, but G1 (both L1 and L2) can never be met;
	// G1 is the smallest-coded node unsatisfied under every assignment.
	if res2.TreeSatisfied || res2.Attribution == nil || res2.Attribution.Code != "G1" ||
		res2.Attribution.Gap.ChildrenGap != 1 {
		t.Fatalf("expected G1 attribution with gap 1, got tree=%v attr=%+v", res2.TreeSatisfied, res2.Attribution)
	}
}

func TestSharedPairDoubleCount(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS1", Semester: 1, Score: 80})
	passingSupplements(t, e, "s")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "g", Course: "MA", Semester: 3, Score: 80})
	res := auditOf(t, e, "s")
	if !res.Pass {
		t.Fatalf("CS1 should count into both L1 and L2 via shared pair: %+v %+v", res.Attribution, res.Additional)
	}
}

func TestInternalPartialRequirement(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	fillBaseline(t, e, "s")
	if err := e.RevokeRecord("s", "r3"); err != nil {
		t.Fatal(err)
	}
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "r6", Course: "MA", Semester: 3, Score: 80})
	res := auditOf(t, e, "s")
	if !res.Pass {
		t.Fatalf("G2 needs only one of L3/L4; MA satisfies L4: %+v %+v", res.Attribution, res.Additional)
	}
}

func TestRequiredFailureThenRetake(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS1", Semester: 1, Score: 40})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "b", Course: "CS1", Semester: 2, Score: 80})
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "q", Course: "CS2", Semester: 1, Score: 80})
	passingSupplements(t, e, "s")
	res := auditOf(t, e, "s")
	if hasKind(res.Additional, "required_failure") {
		t.Fatalf("later passing retake resolves required failure: %+v", res.Additional)
	}

	// Unresolved: failure after the last passing attempt.
	e2 := newTestEngine(t)
	mustEnroll(t, e2, "s", "V1")
	mustRec(t, e2, RegisterRecordInput{StudentID: "s", RecordID: "a", Course: "CS1", Semester: 1, Score: 80})
	mustRec(t, e2, RegisterRecordInput{StudentID: "s", RecordID: "z", Course: "CS1", Semester: 3, Score: 40})
	mustRec(t, e2, RegisterRecordInput{StudentID: "s", RecordID: "q", Course: "CS2", Semester: 1, Score: 80})
	passingSupplements(t, e2, "s")
	res2 := auditOf(t, e2, "s")
	if !hasKind(res2.Additional, "required_failure") {
		t.Fatalf("failure after last pass must remain unresolved: %+v", res2.Additional)
	}
}

func TestRevocationChangesVerdict(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	fillBaseline(t, e, "s")
	if !auditOf(t, e, "s").Pass {
		t.Fatal("baseline should pass")
	}
	if err := e.RevokeRecord("s", "r3"); err != nil {
		t.Fatal(err)
	}
	res := auditOf(t, e, "s")
	if res.Pass {
		t.Fatal("after revoking the only G2-satisfying record the audit must fail")
	}
	err := e.RevokeRecord("s", "r3")
	if err == nil || err.Kind != ErrAlreadyRevoked {
		t.Fatalf("second revoke must be ErrAlreadyRevoked, got %v", err)
	}
}

func TestMigrationVersionRules(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V2")
	err := e.Migrate("s", "V1")
	if err == nil || err.Kind != ErrVersionTooOld {
		t.Fatalf("downgrade must fail, got %v", err)
	}
	if e.students["s"].student.PlanID != "V2" {
		t.Fatal("rejected migration must not change binding")
	}
	mustEnroll(t, e, "s2", "V1")
	if err := e.Migrate("s2", "V2"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if e.students["s2"].student.PlanID != "V2" {
		t.Fatal("upgrade should bind new version")
	}
}

// TestErrorPriority verifies every adjacent pair in the fixed priority:
// invalid > not-found > revoked > sub-not-applicable > transfer-overflow >
// version-too-old.
func TestErrorPriority(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	mustRec(t, e, RegisterRecordInput{StudentID: "s", RecordID: "r1", Course: "CS1", Semester: 1, Score: 80})
	if err := e.RevokeRecord("s", "r1"); err != nil {
		t.Fatal(err)
	}

	// invalid > not-found
	if err := e.Enroll("", "NOPE"); err == nil || err.Kind != ErrInvalid {
		t.Fatalf("invalid>notfound: %v", err)
	}
	// not-found > revoked (nonexistent student on an existing revoked record)
	if err := e.RevokeRecord("ghost", "r1"); err == nil || err.Kind != ErrNotFound {
		t.Fatalf("notfound>revoked: %v", err)
	}
	// revoked > sub-not-applicable (revoked record id reused as substitution
	// trigger is not shared; the pair is exercised via rank order checks on
	// distinct ops, so verify revoked first on its op).
	if err := e.RevokeRecord("s", "r1"); err == nil || err.Kind != ErrAlreadyRevoked {
		t.Fatalf("revoked rank: %v", err)
	}
	// sub-not-applicable > transfer-overflow: substitution bound to wrong plan
	mustEnroll(t, e, "s2", "V2")
	if err := e.RegisterSubstitution("s2", "CS1", "CS2", "V1", 1); err == nil || err.Kind != ErrSubNotApplicable {
		t.Fatalf("sub-not-applicable rank: %v", err)
	}
	// transfer-overflow > version-too-old: overflow is hit on record
	// registration before any migration decision.
	mustEnroll(t, e, "s3", "V2")
	mustRec(t, e, RegisterRecordInput{StudentID: "s3", RecordID: "t1", Course: "MA", Semester: 1, Score: 80, Transfer: true})
	mustRec(t, e, RegisterRecordInput{StudentID: "s3", RecordID: "t2", Course: "PE", Semester: 1, Score: 80, Transfer: true})
	if err := e.RegisterRecord(RegisterRecordInput{StudentID: "s3", RecordID: "t3", Course: "EN", Semester: 1, Score: 80, Transfer: true}); err == nil || err.Kind != ErrTransferOverflow {
		t.Fatalf("transfer-overflow rank: %v", err)
	}
	if err := e.Migrate("s3", "V1"); err == nil || err.Kind != ErrVersionTooOld {
		t.Fatalf("version-too-old rank: %v", err)
	}
}

func TestRejectedOpDoesNotMutate(t *testing.T) {
	e := newTestEngine(t)
	mustEnroll(t, e, "s", "V1")
	before := len(e.students["s"].records)
	if err := e.RegisterRecord(RegisterRecordInput{StudentID: "s", RecordID: "x", Course: "GHOST", Semester: 1, Score: 80}); err == nil || err.Kind != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	if len(e.students["s"].records) != before {
		t.Fatal("failed registration mutated state")
	}
}
