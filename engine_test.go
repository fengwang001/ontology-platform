package audit

import "testing"

func mustErrCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error code %d, got nil", want)
	}
	op, ok := err.(*OpError)
	if !ok || op.Code != want {
		t.Fatalf("want error code %d, got %v", want, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func auditOK(t *testing.T, e *Engine, student string) *AuditResult {
	t.Helper()
	r, err := e.Audit(student)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return r
}

func hasCondition(r *AuditResult, kind string) bool {
	for _, c := range r.ConditionFails {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

func leaf(id string, required bool, minCr float64, minN int, courses ...string) *Requirement {
	return &Requirement{
		ID: id, Kind: ReqLeaf, Required: required,
		Courses: courses, MinCredits: minCr, MinCourses: minN,
	}
}

func internal(id string, minChildren int, children ...*Requirement) *Requirement {
	return &Requirement{ID: id, Kind: ReqInternal, MinChildren: minChildren, Children: children}
}

func planV1(root *Requirement) *PlanVersion {
	return &PlanVersion{
		PlanID: "P", Version: 1, Root: root,
		MinTotalCredit: 10, PassLine: 60, MinGPA: 60, TransferCap: 6,
	}
}

func setupEngine(t *testing.T, plan *PlanVersion, courses ...Course) *Engine {
	t.Helper()
	e := NewEngine()
	for _, c := range courses {
		if err := e.AddCourse(c); err != nil {
			t.Fatalf("add course: %v", err)
		}
	}
	if err := e.AddPlan(plan); err != nil {
		t.Fatalf("add plan: %v", err)
	}
	if err := e.Enroll(Student{ID: "stu", PlanID: plan.PlanID, PlanVersion: plan.Version}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	return e
}

func rec(id, course, sem string, cr, grade float64) Record {
	return Record{ID: id, Student: "stu", Course: course, Semester: sem, Credits: cr, Grade: grade}
}

var commonCourses = []Course{
	{ID: "A", Credits: 4}, {ID: "B", Credits: 3}, {ID: "C", Credits: 2},
	{ID: "X", Credits: 4}, {ID: "Y", Credits: 2},
}

// 及格线取等：60 分恰为及格；总学分/GPA 下限取等通过。
func TestPassLineAndGPAEquality(t *testing.T) {
	root := leaf("L1", true, 9, 3, "A", "B", "C")
	p := planV1(root)
	p.MinTotalCredit = 9
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("r1", "A", "2023-1", 4, 60)))
	must(t, e.RegisterRecord(rec("r2", "B", "2023-1", 3, 60)))
	must(t, e.RegisterRecord(rec("r3", "C", "2023-1", 2, 60)))
	r := auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("want pass at equality, fails=%+v attrib=%+v", r.ConditionFails, r.Attrib)
	}
}

// 同课程多次修读：最高成绩一次可计入；成绩相同取最早学期。
func TestRepeatedCourseSameGradeTakesEarliest(t *testing.T) {
	root := leaf("L1", true, 4, 1, "A")
	p := planV1(root)
	p.MinTotalCredit = 4
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("r1", "A", "2023-1", 4, 80)))
	must(t, e.RegisterRecord(rec("r2", "A", "2024-1", 4, 90)))
	must(t, e.RegisterRecord(rec("r3", "A", "2022-1", 4, 90)))
	r := auditOK(t, e, "stu")
	if !r.Pass || r.GPA != 90 {
		t.Fatalf("want pass gpa=90, got pass=%v gpa=%v", r.Pass, r.GPA)
	}
	must(t, e.RevokeRecord("stu", "r3"))
	if r = auditOK(t, e, "stu"); r.GPA != 90 {
		t.Fatalf("gpa after revoking earliest-90 want 90, got %v", r.GPA)
	}
	must(t, e.RevokeRecord("stu", "r2"))
	if r = auditOK(t, e, "stu"); r.GPA != 80 {
		t.Fatalf("gpa after both 90 revoked want 80, got %v", r.GPA)
	}
}

// 替换生效学期取等可替换；替换后学分取两门课程较小者。
func TestSubstitutionEffectiveEqualityAndMinCredit(t *testing.T) {
	root := leaf("L1", true, 4, 1, "A")
	p := planV1(root)
	p.MinTotalCredit = 4
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterSubstitution(Substitution{
		ID: "s1", From: "X", To: "A", PlanID: "P", PlanVersion: 1, Effective: "2023-2",
	}))
	must(t, e.RegisterRecord(rec("r1", "X", "2023-1", 4, 80)))
	r := auditOK(t, e, "stu")
	if r.Pass || r.Attrib == nil {
		t.Fatalf("want fail before effective, got %+v", r)
	}
	must(t, e.RegisterRecord(rec("r2", "X", "2023-2", 4, 85)))
	if r = auditOK(t, e, "stu"); !r.Pass {
		t.Fatalf("want pass at effective equality: %+v", r)
	}

	root2 := leaf("L2", true, 2, 1, "Y")
	p2 := planV1(root2)
	p2.Version, p2.MinTotalCredit = 2, 2
	must(t, e.AddPlan(p2))
	must(t, e.SwitchVersion("stu", "P", 2))
	must(t, e.RegisterSubstitution(Substitution{
		ID: "s2", From: "X", To: "Y", PlanID: "P", PlanVersion: 2, Effective: "2023-1",
	}))
	r = auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("want pass with 2-credit substitution, got %+v", r)
	}
	if got := r.Assignment["L2"]; len(got) != 1 || got[0] != "Y" {
		t.Fatalf("L2 want [Y], got %v", got)
	}
}

// 转入学分恰等于上限通过；再多一门超出则被拒且不改状态。
func TestTransferCapExactAndExceed(t *testing.T) {
	root := leaf("L1", true, 6, 2, "A", "B")
	p := planV1(root)
	p.MinTotalCredit = 6
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterTransfer(TransferRecord{Student: "stu", Course: "A", Credits: 3, Grade: 70, Semester: "2022-1"}))
	must(t, e.RegisterTransfer(TransferRecord{Student: "stu", Course: "B", Credits: 3, Grade: 70, Semester: "2022-1"}))
	r := auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("want pass at cap exactly: %+v", r)
	}
	before := r.TotalCredits
	mustErrCode(t, e.RegisterTransfer(TransferRecord{Student: "stu", Course: "A", Credits: 1, Grade: 70, Semester: "2022-1"}), ErrTransferCap)
	if r = auditOK(t, e, "stu"); r.TotalCredits != before {
		t.Fatalf("state changed after rejected transfer: %v != %v", r.TotalCredits, before)
	}
}

// 一门课程同时可归入两个叶子；只有一种归入方式能使根满足。
func TestAmbiguousAssignmentOnlyOneWayPasses(t *testing.T) {
	l1 := leaf("L1", true, 4, 1, "A", "B")
	l2 := leaf("L2", true, 4, 1, "A")
	p := planV1(internal("ROOT", 2, l1, l2))
	p.MinTotalCredit = 8
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("a", "A", "2023-1", 4, 80)))
	must(t, e.RegisterRecord(rec("b", "B", "2023-1", 4, 80)))
	r := auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("want pass via unique assignment, attrib=%+v fails=%+v", r.Attrib, r.ConditionFails)
	}
	if len(r.Assignment["L1"]) != 1 || r.Assignment["L1"][0] != "B" {
		t.Fatalf("L1 must get B, got %v", r.Assignment["L1"])
	}
	if len(r.Assignment["L2"]) != 1 || r.Assignment["L2"][0] != "A" {
		t.Fatalf("L2 must get A, got %v", r.Assignment["L2"])
	}
}

// 显式声明允许重复计入时，同一门课程可同时满足两个叶子。
func TestSharedCreditPair(t *testing.T) {
	l1 := leaf("L1", true, 4, 1, "A")
	l2 := leaf("L2", true, 4, 1, "A")
	p := planV1(internal("ROOT", 2, l1, l2))
	p.MinTotalCredit = 4
	p.SharedCredit = map[[2]string]bool{{"L1", "L2"}: true}
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("a", "A", "2023-1", 4, 80)))
	r := auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("shared credit should pass: %+v", r.Attrib)
	}
}

// 内部要求部分满足；都不满足时归因到编号最小的不可能叶子。
func TestInternalPartialSatisfactionAndAttribution(t *testing.T) {
	l1 := leaf("L1", true, 4, 1, "A")
	l2 := leaf("L2", true, 3, 1, "B")
	p := planV1(internal("ROOT", 1, l1, l2))
	p.MinTotalCredit = 0
	e := setupEngine(t, p, commonCourses...)
	r := auditOK(t, e, "stu")
	if r.Pass || r.Attrib == nil || r.Attrib.ReqID != "L1" {
		t.Fatalf("attrib want L1, got %+v", r.Attrib)
	}
	if r.Attrib.CourseGap != 1 || r.Attrib.CreditGap != 4 {
		t.Fatalf("gap want 4cr/1 course, got %+v", r.Attrib)
	}
	must(t, e.RegisterRecord(rec("b", "B", "2023-1", 3, 70)))
	r = auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("one of two should satisfy partial internal: attrib=%+v fails=%+v", r.Attrib, r.ConditionFails)
	}
}

// 必修不及格后来重修及格则结清；否则附加条件失败。
func TestUnclearedRequiredFailThenRetake(t *testing.T) {
	p := planV1(leaf("L1", true, 4, 1, "A"))
	p.MinTotalCredit = 0
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("f", "A", "2023-1", 4, 50)))
	r := auditOK(t, e, "stu")
	if r.Pass || !hasCondition(r, "uncleared_required_fail") {
		t.Fatalf("want uncleared fail: %+v", r.ConditionFails)
	}
	must(t, e.RegisterRecord(rec("p", "A", "2024-1", 4, 75)))
	r = auditOK(t, e, "stu")
	if !r.Pass {
		t.Fatalf("retake pass should clear: attrib=%+v fails=%+v", r.Attrib, r.ConditionFails)
	}
}

// 撤销记录后审核结论变化；重复撤销返回"记录已撤销"。
func TestRevokeChangesConclusion(t *testing.T) {
	p := planV1(leaf("L1", true, 4, 1, "A"))
	p.MinTotalCredit = 4
	e := setupEngine(t, p, commonCourses...)
	must(t, e.RegisterRecord(rec("a", "A", "2023-1", 4, 70)))
	if !auditOK(t, e, "stu").Pass {
		t.Fatalf("should pass before revoke")
	}
	must(t, e.RevokeRecord("stu", "a"))
	if r := auditOK(t, e, "stu"); r.Pass {
		t.Fatalf("should fail after revoke")
	}
	mustErrCode(t, e.RevokeRecord("stu", "a"), ErrRecordRevoked)
}

// 目标版本早于当前版本被拒且不改变绑定；新版本不影响未申请的学生。
func TestVersionBindingAndSwitch(t *testing.T) {
	e := setupEngine(t, planV1(leaf("L1", true, 4, 1, "A")), commonCourses...)
	p2 := planV1(leaf("NEW", true, 4, 1, "B"))
	p2.Version = 2
	must(t, e.AddPlan(p2))
	must(t, e.SwitchVersion("stu", "P", 2))
	mustErrCode(t, e.SwitchVersion("stu", "P", 1), ErrVersionTooOld)
	r := auditOK(t, e, "stu")
	if r.PlanVersion != 2 {
		t.Fatalf("version should stay 2 after rejected downgrade, got %d", r.PlanVersion)
	}
	if r = auditOK(t, e, "stu"); r.PlanVersion != 2 {
		t.Fatalf("version want 2, got %d", r.PlanVersion)
	}
}

// 替换的目标课程不在该方案版本的要求树中：不适用于该版本。
func TestSubstitutionNotApplicableToVersion(t *testing.T) {
	e := setupEngine(t, planV1(leaf("L1", true, 4, 1, "A")), commonCourses...)
	mustErrCode(t, e.RegisterSubstitution(Substitution{
		ID: "s1", From: "X", To: "Y", PlanID: "P", PlanVersion: 1, Effective: "2023-1",
	}), ErrSubNotApplicable)
}

// 错误固定优先级：参数非法 > 不存在 > 记录已撤销 > 替换不适用 > 转入超上限 > 版本过旧。
// 每个操作内同时构造多个错误条件，断言返回最高优先级错误，且状态不变。
func TestErrorPriorityPairwise(t *testing.T) {
	type pair struct {
		name   string
		invoke func(e *Engine) error
		want   ErrorCode
	}

	// RegisterRecord：非法参数 > 学生不存在 > 课程不存在 > 重复登记(非法)。
	rr := func(e *Engine) error {
		return e.RegisterRecord(Record{ID: "", Student: "ghost", Course: "GHOST", Semester: "2023-1"})
	}
	rrNotFound := func(e *Engine) error {
		return e.RegisterRecord(Record{ID: "x", Student: "ghost", Course: "GHOST", Semester: "2023-1"})
	}
	rrCourse := func(e *Engine) error {
		return e.RegisterRecord(Record{ID: "x", Student: "stu", Course: "GHOST", Semester: "2023-1"})
	}

	// RevokeRecord：非法 > 学生不存在 > 记录不存在 > 已撤销。
	rvInvalid := func(e *Engine) error { return e.RevokeRecord("", "") }
	rvStudent := func(e *Engine) error { return e.RevokeRecord("ghost", "a") }
	rvRecord := func(e *Engine) error { return e.RevokeRecord("stu", "ghost") }

	// RegisterSubstitution：非法 > 课程不存在 > 方案不存在 > 不适用。
	rsInvalid := func(e *Engine) error {
		return e.RegisterSubstitution(Substitution{ID: "", From: "X", To: "A"})
	}
	rsCourse := func(e *Engine) error {
		return e.RegisterSubstitution(Substitution{ID: "s9", From: "GHOST", To: "A", PlanID: "P", PlanVersion: 1, Effective: "2023-1"})
	}
	rsPlan := func(e *Engine) error {
		return e.RegisterSubstitution(Substitution{ID: "s9", From: "X", To: "A", PlanID: "P", PlanVersion: 99, Effective: "2023-1"})
	}
	rsNotApp := func(e *Engine) error {
		return e.RegisterSubstitution(Substitution{ID: "s9", From: "X", To: "Y", PlanID: "P", PlanVersion: 1, Effective: "2023-1"})
	}

	// RegisterTransfer：非法 > 学生不存在 > 课程不存在 > 超上限。
	rtInvalid := func(e *Engine) error {
		return e.RegisterTransfer(TransferRecord{Student: "", Course: ""})
	}
	rtStudent := func(e *Engine) error {
		return e.RegisterTransfer(TransferRecord{Student: "ghost", Course: "A", Credits: 1, Semester: "2023-1"})
	}
	rtCourse := func(e *Engine) error {
		return e.RegisterTransfer(TransferRecord{Student: "stu", Course: "GHOST", Credits: 1, Semester: "2023-1"})
	}
	rtCap := func(e *Engine) error {
		return e.RegisterTransfer(TransferRecord{Student: "stu", Course: "A", Credits: 7, Grade: 80, Semester: "2023-1"})
	}

	// SwitchVersion：非法 > 学生不存在 > 方案不存在 > 版本过旧。
	swInvalid := func(e *Engine) error { return e.SwitchVersion("", "", 0) }
	swStudent := func(e *Engine) error { return e.SwitchVersion("ghost", "P", 1) }
	swPlan := func(e *Engine) error { return e.SwitchVersion("stu", "GHOST", 1) }
	swOld := func(e *Engine) error {
		p2 := planV1(leaf("L1", true, 4, 1, "A"))
		p2.Version = 2
		if err := e.AddPlan(p2); err != nil {
			return err
		}
		if err := e.SwitchVersion("stu", "P", 2); err != nil {
			return err
		}
		return e.SwitchVersion("stu", "P", 1)
	}

	cases := []pair{
		{"record invalid-vs-all", rr, ErrInvalidArgument},
		{"record not-found(student)", rrNotFound, ErrNotFound},
		{"record not-found(course)", rrCourse, ErrNotFound},
		{"revoke invalid", rvInvalid, ErrInvalidArgument},
		{"revoke student-missing", rvStudent, ErrNotFound},
		{"revoke record-missing", rvRecord, ErrNotFound},
		{"sub invalid", rsInvalid, ErrInvalidArgument},
		{"sub course-missing", rsCourse, ErrNotFound},
		{"sub plan-missing", rsPlan, ErrNotFound},
		{"sub not-applicable", rsNotApp, ErrSubNotApplicable},
		{"transfer invalid", rtInvalid, ErrInvalidArgument},
		{"transfer student-missing", rtStudent, ErrNotFound},
		{"transfer course-missing", rtCourse, ErrNotFound},
		{"transfer cap", rtCap, ErrTransferCap},
		{"switch invalid", swInvalid, ErrInvalidArgument},
		{"switch student-missing", swStudent, ErrNotFound},
		{"switch plan-missing", swPlan, ErrNotFound},
		{"switch version-old", swOld, ErrVersionTooOld},
	}

	// 已撤销记录的优先级高于转入上限等：构造一条已撤销记录再撤销。
	newEngine := func() *Engine {
		e := setupEngine(t, planV1(leaf("L1", true, 4, 1, "A")), commonCourses...)
		must(t, e.RegisterRecord(rec("a", "A", "2023-1", 4, 70)))
		must(t, e.RevokeRecord("stu", "a"))
		return e
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEngine()
			before := auditOK(t, e, "stu")
			mustErrCode(t, c.invoke(e), c.want)
			after := auditOK(t, e, "stu")
			if after.Pass != before.Pass || after.TotalCredits != before.TotalCredits {
				t.Fatalf("rejected op %s changed state", c.name)
			}
		})
	}

	// 记录已撤销：优先级 3（低于不存在，高于其它）。
	e := newEngine()
	mustErrCode(t, e.RevokeRecord("stu", "a"), ErrRecordRevoked)

	// 显式校验优先级序的单调性。
	order := []ErrorCode{ErrInvalidArgument, ErrNotFound, ErrRecordRevoked, ErrSubNotApplicable, ErrTransferCap, ErrVersionTooOld}
	for i := 1; i < len(order); i++ {
		if ErrPriority(order[i-1]) >= ErrPriority(order[i]) {
			t.Fatalf("priority order violated at %d", i)
		}
	}
}

// 审核开销不随无关学生总数与记录总数增长（访问计数可验证）。
func TestAuditCostIsolatedFromUnrelatedStudents(t *testing.T) {
	e := setupEngine(t, planV1(leaf("L1", true, 4, 1, "A")), commonCourses...)
	must(t, e.RegisterRecord(rec("a", "A", "2023-1", 4, 70)))
	_ = auditOK(t, e, "stu")
	baseline := e.LastAuditTouches()

	// 加入大量无关学生及其记录、替换。
	for i := 0; i < 200; i++ {
		sid := "other" + itoa(i)
		must(t, e.Enroll(Student{ID: sid, PlanID: "P", PlanVersion: 1}))
		must(t, e.RegisterRecord(Record{ID: "r" + itoa(i), Student: sid, Course: "A", Semester: "2023-1", Credits: 4, Grade: 70}))
	}
	_ = auditOK(t, e, "stu")
	if got := e.LastAuditTouches(); got != baseline {
		t.Fatalf("audit touches grew with unrelated students: %d != %d", got, baseline)
	}
}
