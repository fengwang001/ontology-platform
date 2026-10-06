package allocation

import "testing"

func codeOf(err *Error) ErrorCode {
	if err == nil {
		return -1
	}
	return err.Code
}

// 错误优先级：参数非法 < 时钟回退 < 不存在 < 状态 < 冲突 < 超上限 < 学时不守恒。
// 每个用例同时触发多个类别，只应报第一个。
func TestErrorPriority(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 10, "t1", "P")
	mustTask(t, s, 11, TaskSpec{
		ID: "c", Semester: "S1", Hours: 2, ClassSize: 1,
		StartWeek: 1, EndWeek: 1, Periods: []int{1},
	})

	// 参数非法 优先于 时钟回退
	if c := codeOf(s.AddTeacher(1, TeacherSpec{ID: "", Rank: "P"})); c != ErrInvalidArgument {
		t.Fatalf("got %v", c)
	}
	// 时钟回退 优先于 不存在
	if _, e := s.AssignBatch(1, []AssignItem{{"ghost", "ghost2", 1}}); codeOf(e) != ErrClockRollback {
		t.Fatalf("got %v", codeOf(e))
	}
	// 不存在 优先于 状态/冲突/守恒：任务、教师都不存在
	if _, e := s.AssignBatch(12, []AssignItem{{"ghost", "t1", 1}}); codeOf(e) != ErrNotFound {
		t.Fatalf("got %v", codeOf(e))
	}
	// 冻结（状态）优先于冲突/上限/守恒：先结算学期
	if _, err := s.Settle(20, "S1"); err != nil {
		t.Fatal(err)
	}
	mustTask(t, s, 21, TaskSpec{
		ID: "c2", Semester: "S1", Hours: 4, ClassSize: 1,
		StartWeek: 1, EndWeek: 2, Periods: []int{1},
	})
	_, err := s.AssignBatch(22, []AssignItem{
		{"c2", "t1", 3}, // 若不冻结：先成功占用；学时和也不等于4
	})
	if err == nil || err.Code != ErrState {
		t.Fatalf("frozen must win, got %v", err)
	}
}

func TestClockRollbackLeavesStateUntouched(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 10, "t1", "P")
	err := s.AddTeacher(5, TeacherSpec{ID: "t2", Rank: "P"})
	if err == nil || err.Code != ErrClockRollback {
		t.Fatalf("want rollback, got %v", err)
	}
	if _, ok := s.Snapshot().Teachers["t2"]; ok {
		t.Fatal("rollback op mutated state")
	}
}

// 相同操作序列重放结果完全一致：对两个服务重放同一脚本，比较错误类别与份额签名。
func TestDeterministicReplay(t *testing.T) {
	script := []func(s *Service) (ErrorCode, int){
		func(s *Service) (ErrorCode, int) { return codeOf(s.AddTeacher(1, TeacherSpec{"a", "P"})), -1 },
		func(s *Service) (ErrorCode, int) { return codeOf(s.AddTeacher(1, TeacherSpec{"b", "P"})), -1 },
		func(s *Service) (ErrorCode, int) {
			return codeOf(s.AddTask(2, TaskSpec{ID: "c", Semester: "S", Hours: 6, ClassSize: 5,
				StartWeek: 1, EndWeek: 3, Periods: []int{1}})), -1
		},
		func(s *Service) (ErrorCode, int) {
			_, e := s.AssignBatch(3, []AssignItem{{"c", "a", 4}, {"c", "b", 3}})
			return codeOf(e), indexOr(e)
		},
		func(s *Service) (ErrorCode, int) {
			_, e := s.AssignBatch(4, []AssignItem{{"c", "a", 3}, {"c", "b", 3}})
			return codeOf(e), indexOr(e)
		},
		func(s *Service) (ErrorCode, int) {
			ids, e := s.AssignBatch(5, []AssignItem{{"c", "a", 2}, {"c", "b", 4}})
			return codeOf(e), firstID(ids)
		},
		func(s *Service) (ErrorCode, int) { return codeOf(s.Confirm(6, 1)), -1 },
		func(s *Service) (ErrorCode, int) { c := codeOf(s.Confirm(6, 1)); return c, -1 },
		func(s *Service) (ErrorCode, int) { _, e := s.ChangeTeacher(7, 1, "b", 2); return codeOf(e), -1 },
	}
	var r1, r2 []string
	var c1, c2 []ErrorCode
	run := func() ([]ErrorCode, []string) {
		s := NewService(testConfig())
		var codes []ErrorCode
		for _, f := range script {
			c, _ := f(s)
			codes = append(codes, c)
		}
		return codes, signature(s)
	}
	c1, r1 = run()
	c2, r2 = run()
	if len(c1) != len(c2) {
		t.Fatal("length")
	}
	for i := range c1 {
		if c1[i] != c2[i] {
			t.Fatalf("op %d codes differ: %d vs %d", i, c1[i], c2[i])
		}
	}
	if stringsJoin(r1) != stringsJoin(r2) {
		t.Fatalf("signatures differ:\n%v\n%v", r1, r2)
	}
}

func indexOr(e *Error) int {
	if e == nil {
		return -1
	}
	return e.Index
}

func firstID(ids []int64) int {
	if len(ids) == 0 {
		return -1
	}
	return int(ids[0])
}

func signature(s *Service) []string {
	snap := s.Snapshot()
	var out []string
	for _, sh := range snap.Shares {
		out = append(out, sh.TaskID+"/"+sh.TeacherID+"/"+sh.State+
			"/"+itoa(sh.StartWeek)+"-"+itoa(sh.EndWeek)+"/h"+itoa(sh.Hours)+"/w"+itoa(sh.Workload))
	}
	return out
}

func stringsJoin(a []string) string {
	out := ""
	for _, x := range a {
		out += x + "|"
	}
	return out
}
