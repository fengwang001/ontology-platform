package teaching

import (
	"testing"
)

func testConfig() Config {
	return Config{
		Tiers: []ScaleTier{
			{MaxSize: 30, Coeff: 100},
			{MaxSize: 60, Coeff: 120}, // 边界取等归高档：60 人命中 120
			{MaxSize: 120, Coeff: 150},
		},
		NewCourseAdd: 10,
		LabCoeff:     110,
		Ranks: map[string]Rank{
			"prof":  {Name: "prof", MinLoad: 100, MaxLoad: 200},
			"lect":  {Name: "lect", MinLoad: 40, MaxLoad: 80},
			"giant": {Name: "giant", MinLoad: 10000, MaxLoad: 100000},
			"wide":  {Name: "wide", MinLoad: 100, MaxLoad: 100000},
		},
		ConfirmTicks: 10,
	}

}

func newSvc(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(testConfig(), 0)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func codeOf(err error) ErrorCode {
	tErr, ok := err.(*Error)
	if !ok || tErr == nil {
		return 0
	}
	return tErr.Code
}

// 1) 规模档位恰等：人数恰为档边界时归高档。
func TestScaleTierBoundaryEqual(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		size  int
		coeff int
	}{
		{29, 100}, {30, 100}, {31, 120}, {60, 120}, {61, 150}, {120, 150},
	}
	for _, c := range cases {
		got, ok := scaleCoeff(cfg.Tiers, c.size)
		if !ok || got != c.coeff {
			t.Fatalf("size %d: got (%d,%v), want %d", c.size, got, ok, c.coeff)
		}
	}
	if _, ok := scaleCoeff(cfg.Tiers, 121); ok {
		t.Fatal("121 must match no tier")
	}
}

// 2) 三系数连乘后一次取整 vs 分步取整必须不同。
// 规模 120%，新开课 110%，实验 110%，学时 7：
// 学时 9：精确 9*120*110*110/10^6 = 13.068 -> 13
// 分步取整：floor(9*1.2)=10，floor(10*1.1)=11，floor(11*1.1)=12
func TestExactProductVsStepwise(t *testing.T) {
	const hours = 9
	exact := exactWorkload(hours, 120, 110, 110)
	step := floorPct(floorPct(floorPct(hours, 120), 110), 110)
	if exact != 13 {
		t.Fatalf("exact = %d, want 13", exact)
	}
	if step == exact {
		t.Fatalf("stepwise %d unexpectedly equals exact %d", step, exact)
	}
	if step != 12 {
		t.Fatalf("stepwise = %d, want 12", step)
	}
}

func floorPct(v, coeff int) int { return v * coeff / 100 }

// 3) 合上学时之和不等于课程总学时：整批拒绝、报守恒错误、不留痕。
func TestCoTeachHoursNotConserved(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 1))
	must(t, s.AddTeacher("b", "giant", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 20, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))

	err := s.Assign([]AssignmentReq{
		{TeacherID: "a", TaskID: "c1", Hours: 12},
		{TeacherID: "b", TaskID: "c1", Hours: 9}, // 12+9 != 20
	}, 3)
	if codeOf(err) != ErrHoursNotConserved {
		t.Fatalf("want ErrHoursNotConserved, got %v", err)
	}
	if e, _ := err.(*Error); e.Index != 0 {
		t.Fatalf("want index 0, got %d", e.Index)
	}
	if n, _ := s.TaskAllocated("c1", 3); n != 0 {
		t.Fatalf("allocated %d after rejected batch, want 0", n)
	}
	if l, _ := s.ActiveLoad("a", 3); l != 0 {
		t.Fatalf("teacher a load %d after rejected batch, want 0", l)
	}
	// 时钟也不得被拒绝操作推进：拒绝发生在时刻 3，查询也停留在 3。
	if s.Now() != 3 {
		t.Fatalf("rejected batch advanced clock to %d, want 3", s.Now())
	}
}

// 守恒成功后再补一批必须仍等于总学时（重复提交不允许超分）。
func TestConservedThenAccepted(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 1))
	must(t, s.AddTeacher("b", "giant", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 20, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.Assign([]AssignmentReq{
		{TeacherID: "a", TaskID: "c1", Hours: 12},
		{TeacherID: "b", TaskID: "c1", Hours: 8},
	}, 3))
	must(t, s.Respond("a", "c1", true, 4))
	must(t, s.Respond("b", "c1", true, 5))
	n, err := s.TaskAllocated("c1", 6)
	must(t, err)
	if n != 20 {
		t.Fatalf("allocated %d, want 20", n)
	}
	views, _ := s.ListAssignments("a", 7)
	if views[0].Status != StatusConfirmed {
		t.Fatalf("a status = %s", views[0].Status)
	}
}

// 4) 换人零头学时归原教师。
// 20 学时 / 6 周 => base=3, rem=2；第 3 周起换人：
// 原教师保留 2*3+2=8，新教师 12，8+12=20。
func TestReplaceRemainderGoesToOriginal(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 1))
	must(t, s.AddTeacher("b", "giant", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 20, ClassSize: 30,
		WeekStart: 1, WeekEnd: 6, Periods: []int{1},
	}, 2))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 20}}, 3))
	must(t, s.Respond("a", "c1", true, 4))
	must(t, s.Replace("a", "c1", "b", 3, 5))

	va, _ := s.ListAssignments("a", 6)
	vb, _ := s.ListAssignments("b", 6)
	if va[0].Hours != 8 || va[0].WeekStart != 1 || va[0].WeekEnd != 2 {
		t.Fatalf("original kept %+v, want hours=8 weeks[1,2]", va[0])
	}
	if vb[0].Hours != 12 || vb[0].WeekStart != 3 || vb[0].WeekEnd != 6 {
		t.Fatalf("new got %+v, want hours=12 weeks[3,6]", vb[0])
	}
	if n, _ := s.TaskAllocated("c1", 7); n != 20 {
		t.Fatalf("conservation broken: %d", n)
	}
	// 原教师第 3 周起时段已释放给新教师：a 空闲、b 占用
	if owner, _ := s.Occupied("a", 3, 1, 8); owner != "" {
		t.Fatalf("a still occupied week3 by %s", owner)
	}
	if owner, _ := s.Occupied("b", 3, 1, 9); owner != "c1" {
		t.Fatalf("b not occupying week3, got %q", owner)
	}
	if owner, _ := s.Occupied("a", 2, 1, 10); owner != "c1" {
		t.Fatalf("a lost week2 occupancy: %q", owner)
	}
}

// 换人冲突失败必须一切原状。
func TestReplaceFailureKeepsState(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 1))
	must(t, s.AddTeacher("b", "giant", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 12, ClassSize: 30,
		WeekStart: 1, WeekEnd: 6, Periods: []int{1},
	}, 2))
	must(t, s.AddTask(TaskSpec{
		ID: "c2", Semester: "2024-1", Hours: 12, ClassSize: 30,
		WeekStart: 1, WeekEnd: 6, Periods: []int{1},
	}, 3))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 12}}, 4))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "b", TaskID: "c2", Hours: 12}}, 5))
	must(t, s.Respond("a", "c1", true, 6))
	must(t, s.Respond("b", "c2", true, 7))
	err := s.Replace("a", "c1", "b", 3, 8)
	if codeOf(err) != ErrSlotConflict {
		t.Fatalf("want conflict, got %v", err)
	}
	va, _ := s.ListAssignments("a", 9)
	if va[0].Hours != 12 || va[0].WeekStart != 1 || va[0].WeekEnd != 6 {
		t.Fatalf("a changed after failed replace: %+v", va[0])
	}
	if owner, _ := s.Occupied("a", 5, 1, 10); owner != "c1" {
		t.Fatalf("a lost occupancy after failed replace: %q", owner)
	}
}

// 5) 超额抵扣：超额一半向下取整结转；下学期可用抵扣封顶为下限 1/4（向下取整）。
func TestSettleCreditCapEqualAndExceed(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "wide", 1)) // 上限足够大；下限 100，封顶 25
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 250, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 250}}, 3))
	must(t, s.Respond("a", "c1", true, 4))
	r, err := s.Settle("a", "2024-1", 5)
	must(t, err)
	if r.Load != 250 || r.MinLoad != 100 || r.Excess != 150 || r.CreditEarned != 75 {
		t.Fatalf("first settle = %+v", r)
	}

	// 下学期工作量 70：欠额 30；转入 75，但封顶 floor(100/4)=25。
	must(t, s.AddTask(TaskSpec{
		ID: "c2", Semester: "2024-2", Hours: 70, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{2},
	}, 6))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c2", Hours: 70}}, 7))
	must(t, s.Respond("a", "c2", true, 8))
	r2, err := s.Settle("a", "2024-2", 9)
	must(t, err)
	if r2.Shortfall != 30 || r2.CreditUsed != 25 || r2.Unmet != 5 || r2.CreditIncoming != 75 {
		t.Fatalf("cap-exceed settle = %+v", r2)
	}

	// 恰等：下限 104 -> 封顶 26；欠额恰为 26 时全部冲抵、未达标 0。
	s2 := newSvc(t)
	cfg2 := testConfig()
	cfg2.Ranks["r104"] = Rank{Name: "r104", MinLoad: 104, MaxLoad: 100000}
	s2.cfg = cfg2
	must(t, s2.AddTeacher("x", "r104", 1))
	must(t, s2.AddTask(TaskSpec{
		ID: "t1", Semester: "2024-1", Hours: 156, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s2.Assign([]AssignmentReq{{TeacherID: "x", TaskID: "t1", Hours: 156}}, 3))
	must(t, s2.Respond("x", "t1", true, 4))
	rx, err := s2.Settle("x", "2024-1", 5)
	must(t, err)
	if rx.CreditEarned != 26 {
		t.Fatalf("earned = %d, want 26", rx.CreditEarned)
	}
	must(t, s2.AddTask(TaskSpec{
		ID: "t2", Semester: "2024-2", Hours: 78, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{2},
	}, 6))
	must(t, s2.Assign([]AssignmentReq{{TeacherID: "x", TaskID: "t2", Hours: 78}}, 7))
	must(t, s2.Respond("x", "t2", true, 8))
	rx2, err := s2.Settle("x", "2024-2", 9)
	must(t, err)
	if rx2.CreditUsed != 26 || rx2.Unmet != 0 {
		t.Fatalf("equal-bound settle = %+v, want used=26 unmet=0", rx2)
	}
}

// 6) 核算幂等：重复核算结果一致，且不推进时钟；核算后该学期指派冻结。
func TestSettleIdempotentAndFrozen(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "lect", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 20, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 20}}, 3))
	must(t, s.Respond("a", "c1", true, 4))
	r1, err := s.Settle("a", "2024-1", 5)
	must(t, err)
	r2, err := s.Settle("a", "2024-1", 1000)
	must(t, err)
	if *r1 != *r2 {
		t.Fatalf("settle not idempotent:\n%+v\n%+v", r1, r2)
	}
	must(t, s.Touch(6)) // 幂等核算未把时钟推进到 1000

	must(t, s.AddTeacher("b", "giant", 7))
	if err := s.Replace("a", "c1", "b", 2, 8); codeOf(err) != ErrIllegalState {
		t.Fatalf("post-settle replace want IllegalState, got %v", err)
	}
}

// 7) 批量失败不留痕。
func TestBatchFailureLeavesNoTrace(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 1))
	must(t, s.AddTeacher("b", "giant", 1))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 10, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.AddTask(TaskSpec{
		ID: "c2", Semester: "2024-1", Hours: 10, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 3))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 10}}, 4))
	must(t, s.Respond("a", "c1", true, 5))

	err := s.Assign([]AssignmentReq{
		{TeacherID: "b", TaskID: "c2", Hours: 10},
		{TeacherID: "a", TaskID: "c2", Hours: 0},
	}, 6)
	if codeOf(err) != ErrInvalidParameter {
		t.Fatalf("want invalid, got %v", err)
	}
	if views, _ := s.ListAssignments("b", 7); len(views) != 0 {
		t.Fatalf("b leaked assignment: %+v", views)
	}

	err = s.Assign([]AssignmentReq{
		{TeacherID: "a", TaskID: "c2", Hours: 10}, // 下标0：冲突
		{TeacherID: "b", TaskID: "c2", Hours: 10}, // 本可成功
	}, 8)
	e := err.(*Error)
	if e.Code != ErrSlotConflict || e.Index != 0 {
		t.Fatalf("want conflict at 0, got %v", err)
	}
	if n, _ := s.TaskAllocated("c2", 9); n != 0 {
		t.Fatalf("c2 leaked allocation %d", n)
	}
}

// 8) 拒绝优先级：参数非法 > 时钟回退 > 不存在 > 状态。
func TestErrorPriority(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "prof", 10))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 10, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 11))

	if err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 0}}, 5); codeOf(err) != ErrInvalidParameter {
		t.Fatalf("priority invalid: %v", err)
	}
	if err := s.Assign([]AssignmentReq{{TeacherID: "ghost", TaskID: "missing", Hours: 1}}, 5); codeOf(err) != ErrClockRollback {
		t.Fatalf("priority clock: %v", err)
	}
	if err := s.Respond("ghost", "c1", true, 12); codeOf(err) != ErrNotFound {
		t.Fatalf("priority notfound: %v", err)
	}
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 10}}, 12))
	if err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 10}}, 13); codeOf(err) != ErrIllegalState {
		t.Fatalf("priority state: %v", err)
	}
}

// 冲突优先于上限。
func TestConflictBeforeOverCap(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "lect", 1)) // 上限 80
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 70, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.AddTask(TaskSpec{
		ID: "c2", Semester: "2024-1", Hours: 70, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 3))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 70}}, 4))
	must(t, s.Respond("a", "c1", true, 5))
	err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c2", Hours: 70}}, 6)
	if codeOf(err) != ErrSlotConflict {
		t.Fatalf("want conflict before cap, got %v", err)
	}
}

// 9) 超时边界：恰等于期限仍有效；严格超过才惰性释放，释放后不可再确认。
func TestTimeoutBoundaryLazyRelease(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 0))
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 10, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 1))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 10}}, 2))
	must(t, s.Respond("a", "c1", true, 12)) // deadline=12，恰等仍有效

	must(t, s.AddTask(TaskSpec{
		ID: "c3", Semester: "2024-1", Hours: 10, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{3},
	}, 30))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c3", Hours: 10}}, 30)) // deadline 40
	owner, err := s.Occupied("a", 1, 3, 40)                                           // 恰等于：仍占用
	must(t, err)
	if owner != "c3" {
		t.Fatalf("at exact deadline slot should remain occupied, got %q", owner)
	}
	owner, err = s.Occupied("a", 1, 3, 41) // 超过：惰性释放
	must(t, err)
	if owner != "" {
		t.Fatalf("c3 slot should be released at 41, got %q", owner)
	}
	if err := s.Respond("a", "c3", true, 42); codeOf(err) != ErrIllegalState {
		t.Fatalf("released assignment must not be confirmable: %v", err)
	}
	if n, _ := s.TaskAllocated("c3", 43); n != 0 {
		t.Fatalf("released c3 allocated=%d, want 0", n)
	}
}

// 被拒绝操作不得推进时钟。
func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "giant", 5))
	err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "missing", Hours: 1}}, 100)
	if codeOf(err) != ErrNotFound {
		t.Fatalf("want notfound, got %v", err)
	}
	if s.Now() != 5 {
		t.Fatalf("clock advanced to %d on rejected op", s.Now())
	}
}

// 上限校验：含待确认指派在内累计不得超职级上限；确认/拒绝随之释放。
func TestCapIncludesPending(t *testing.T) {
	s := newSvc(t)
	must(t, s.AddTeacher("a", "lect", 1)) // 上限 80
	must(t, s.AddTask(TaskSpec{
		ID: "c1", Semester: "2024-1", Hours: 60, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{1},
	}, 2))
	must(t, s.AddTask(TaskSpec{
		ID: "c2", Semester: "2024-1", Hours: 30, ClassSize: 30,
		WeekStart: 1, WeekEnd: 10, Periods: []int{2},
	}, 3))
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c1", Hours: 60}}, 4))
	err := s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c2", Hours: 30}}, 5)
	if codeOf(err) != ErrOverCap {
		t.Fatalf("pending must count toward cap, got %v", err)
	}
	must(t, s.Respond("a", "c1", false, 6)) // 拒绝后释放上限
	must(t, s.Assign([]AssignmentReq{{TeacherID: "a", TaskID: "c2", Hours: 30}}, 7))
}
