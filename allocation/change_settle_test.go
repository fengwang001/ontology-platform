package allocation

import "testing"

// 换人后零头学时归原教师，两人学时之和恒等于课程学时。
func TestChangeTeacherRemainder(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "old", "P")
	mustTeacher(t, s, 1, "nw", "P")
	// 17 学时 / 5 周 = 每周 3，余 2；零头归第 1、2 周（原教师侧）。
	mustTask(t, s, 2, TaskSpec{
		ID: "c", Semester: "S1", Hours: 17, ClassSize: 10,
		StartWeek: 1, EndWeek: 5, Periods: []int{1},
	})
	ids, err := s.AssignBatch(3, []AssignItem{{"c", "old", 17}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(4, ids[0]); err != nil {
		t.Fatal(err)
	}
	// 从第 3 周换人：原教师得到第1..2周 = 3+3+1+1 = 8；新教师第3..5周 = 3*3 = 9。
	nid, err := s.ChangeTeacher(5, ids[0], "nw", 3)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	var oldH, newH int
	for _, sh := range snap.Shares {
		if sh.ID == ids[0] {
			oldH = sh.Hours
		}
		if sh.ID == nid {
			newH = sh.Hours
		}
	}
	if oldH != 8 || newH != 9 || oldH+newH != 17 {
		t.Fatalf("split old=%d new=%d sum=%d", oldH, newH, oldH+newH)
	}
	// 原教师时段只应剩第 1-2 周；再给新教师一个第 1-2 周节次 1 的任务不应冲突于新教师
	// （新教师只占 3-5 周），但会与原教师冲突。
	mustTask(t, s, 6, TaskSpec{
		ID: "c2", Semester: "S1", Hours: 4, ClassSize: 10,
		StartWeek: 1, EndWeek: 2, Periods: []int{1},
	})
	if _, err := s.AssignBatch(7, []AssignItem{{"c2", "old", 4}}); err == nil ||
		err.Code != ErrConflict {
		t.Fatalf("old teacher conflict expected, got %v", err)
	}
	if _, err := s.AssignBatch(8, []AssignItem{{"c2", "nw", 4}}); err != nil {
		t.Fatalf("new teacher free in weeks 1-2: %v", err)
	}
	// 换给不存在教师 / 越界周次 -> 原状保持
	if _, err := s.ChangeTeacher(9, nid, "ghost", 4); err == nil || err.Code != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

// 超额一半结转、下限四分之一封顶，恰等与超出各一例。
func TestSettleCreditCaps(t *testing.T) {
	cfg := testConfig()
	s := NewService(cfg)
	mustTeacher(t, s, 1, "exact", "P") // min=100 cap4=25, max=300
	mustTeacher(t, s, 1, "over", "P")
	mustTeacher(t, s, 1, "low", "P")
	// exact: total=148 -> excess=48, floor(48/2)=24 <= 25 -> 24
	putHours(t, s, 2, "S1", "te", "exact", 148, 10, 1)
	// over: total=200 -> excess=100, floor(/2)=50 但 cap=25 -> 25（超出作废）
	putHours(t, s, 10, "S1", "to", "over", 200, 10, 2)
	// low: total=80 -> deficit=20
	putHours(t, s, 20, "S1", "tl", "low", 80, 10, 3)
	rep, err := s.Settle(30, "S1")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]TeacherReport{}
	for _, r := range rep.Teachers {
		by[r.TeacherID] = r
	}
	if by["exact"].CreditNext != 24 {
		t.Fatalf("exact credit=%d want 24", by["exact"].CreditNext)
	}
	if by["over"].CreditNext != 25 {
		t.Fatalf("over credit=%d want 25 (quarter cap)", by["over"].CreditNext)
	}
	if by["low"].Unmet != 20 || by["low"].Deficit != 20 {
		t.Fatalf("low unmet=%d", by["low"].Unmet)
	}

	// 恰等边界：excess=50 -> floor(/2)=25 == cap，允许 25
	s2 := NewService(cfg)
	mustTeacher(t, s2, 1, "e", "P")
	putHours(t, s2, 2, "S1", "t", "e", 150, 10, 5)
	r2, err := s2.Settle(10, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Teachers[0].CreditNext != 25 {
		t.Fatalf("equal-boundary credit=%d want 25", r2.Teachers[0].CreditNext)
	}
}

// 欠额由上学期抵扣冲抵；抵扣不能抬高上限；上限四分之一封顶。
func TestSettleCarryForward(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "a", "P") // min=100 quarter cap=25
	// 上学期 total=160 -> excess=60 -> credit=25（被封）
	putHours(t, s, 2, "S1", "t1", "a", 160, 10, 1)
	if _, err := s.Settle(5, "S1"); err != nil {
		t.Fatal(err)
	}
	// 本学期 total=60 -> deficit=40；带入 25 但 cap=25 -> used=25, unmet=15
	putHours(t, s, 6, "S2", "t2", "a", 60, 10, 2)
	rep, err := s.Settle(9, "S2")
	if err != nil {
		t.Fatal(err)
	}
	var r TeacherReport
	for _, x := range rep.Teachers {
		if x.TeacherID == "a" {
			r = x
		}
	}
	if r.CreditBrought != 25 || r.CreditUsed != 25 || r.Unmet != 15 {
		t.Fatalf("brought=%d used=%d unmet=%d", r.CreditBrought, r.CreditUsed, r.Unmet)
	}
	if r.Total != 60 {
		t.Fatalf("credit must not inflate total: %d", r.Total)
	}
	// 幂等：重复核算结果相同且时钟不变
	before := s.Snapshot().Clock
	rep2, err := s.Settle(12345, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if rep2.SettledAt != 5 || s.Snapshot().Clock != before {
		t.Fatalf("idempotent settle changed state: %d", s.Snapshot().Clock)
	}
	// 核算后冻结：指派与换人均被拒
	mustTask(t, s, 200, TaskSpec{ID: "tx", Semester: "S1", Hours: 2, ClassSize: 1,
		StartWeek: 1, EndWeek: 1, Periods: []int{9}})
	_, err = s.AssignBatch(201, []AssignItem{{"tx", "a", 2}})
	if err == nil || err.Code != ErrState {
		t.Fatalf("frozen assign want ErrState, got %v", err)
	}
}

func putHours(t *testing.T, s *Service, now int64, semester, taskID, teacher string, hours, size int, period int) {
	t.Helper()
	mustTask(t, s, now, TaskSpec{
		ID: taskID, Semester: semester, Hours: hours, ClassSize: size,
		StartWeek: 1, EndWeek: 1, Periods: []int{period},
	})
	ids, err := s.AssignBatch(now+1, []AssignItem{{taskID, teacher, hours}})
	if err != nil {
		t.Fatalf("putHours assign: %v", err)
	}
	if err := s.Confirm(now+2, ids[0]); err != nil {
		t.Fatalf("putHours confirm: %v", err)
	}
}
