package allocation

import (
	"fmt"
	"testing"
)

type logTracer struct {
	t     *testing.T
	lines *[]string
	echo  bool
}

func (l logTracer) Logf(format string, args ...any) {
	msg := sprintf(format, args...)
	*l.lines = append(*l.lines, msg)
	if l.echo {
		l.t.Log(msg)
	}
}

func sprintf(f string, args ...any) string {
	return fmt.Sprintf(f, args...)
}

func mustTeacher(t *testing.T, s *Service, now int64, id, rank string) {
	t.Helper()
	if err := s.AddTeacher(now, TeacherSpec{ID: id, Rank: rank}); err != nil {
		t.Fatalf("AddTeacher %s: %v", id, err)
	}
}

func mustTask(t *testing.T, s *Service, now int64, spec TaskSpec) {
	t.Helper()
	if err := s.AddTask(now, spec); err != nil {
		t.Fatalf("AddTask %s: %v", spec.ID, err)
	}
}

func TestCoTeachHoursConservation(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "t1", "P")
	mustTeacher(t, s, 1, "t2", "P")
	mustTask(t, s, 2, TaskSpec{
		ID: "c1", Semester: "S1", Hours: 16, ClassSize: 40,
		StartWeek: 1, EndWeek: 8, Periods: []int{1},
	})
	// 10+5=15 != 16 -> ErrHoursConservation
	_, err := s.AssignBatch(3, []AssignItem{
		{TaskID: "c1", TeacherID: "t1", Hours: 10},
		{TaskID: "c1", TeacherID: "t2", Hours: 5},
	})
	if err == nil || err.Code != ErrHoursConservation {
		t.Fatalf("want conservation error, got %v", err)
	}
	// 失败不留痕：没有任何份额
	snap := s.Snapshot()
	if len(snap.Shares) != 0 {
		t.Fatalf("shares leaked: %+v", snap.Shares)
	}
	if snap.Clock != 2 {
		t.Fatalf("clock moved on rejected op: %d", snap.Clock)
	}
	// 守恒的重新指派应成功
	ids, err := s.AssignBatch(4, []AssignItem{
		{TaskID: "c1", TeacherID: "t1", Hours: 10},
		{TaskID: "c1", TeacherID: "t2", Hours: 6},
	})
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
}

func TestSlotConflictAndCap(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "t1", "P") // cap 300
	mustTask(t, s, 2, TaskSpec{
		ID: "c1", Semester: "S1", Hours: 16, ClassSize: 30, // scale 110
		StartWeek: 1, EndWeek: 8, Periods: []int{1},
	})
	mustTask(t, s, 2, TaskSpec{
		ID: "c2", Semester: "S1", Hours: 16, ClassSize: 30,
		StartWeek: 1, EndWeek: 8, Periods: []int{1},
	})
	mustTask(t, s, 2, TaskSpec{
		ID: "c3", Semester: "S1", Hours: 16, ClassSize: 30,
		StartWeek: 1, EndWeek: 8, Periods: []int{2},
	})
	if _, err := s.AssignBatch(3, []AssignItem{{"c1", "t1", 16}}); err != nil {
		t.Fatal(err)
	}
	// 同节次同周 -> 冲突
	_, err := s.AssignBatch(4, []AssignItem{{"c2", "t1", 16}})
	if err == nil || err.Code != ErrConflict {
		t.Fatalf("want conflict, got %v", err)
	}
	// 不同节次可指派
	if _, err := s.AssignBatch(5, []AssignItem{{"c3", "t1", 16}}); err != nil {
		t.Fatalf("different period should pass: %v", err)
	}
	// 每份 wl = floor(16*110/100)=17；两份=34，未超 300。构造超限：
	s2 := NewService(testConfig())
	mustTeacher(t, s2, 1, "lo", "P")
	mustTask(t, s2, 2, TaskSpec{
		ID: "big", Semester: "S1", Hours: 300, ClassSize: 200, IsLab: true, // 150*150/1e4
		StartWeek: 1, EndWeek: 1, Periods: []int{1},
	})
	_, err = s2.AssignBatch(3, []AssignItem{{"big", "lo", 300}})
	if err == nil || err.Code != ErrOverCap {
		t.Fatalf("want over cap, got %v", err)
	}
}

func TestBatchAllOrNothingSmallestIndex(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "t1", "P")
	mustTask(t, s, 2, TaskSpec{
		ID: "c1", Semester: "S1", Hours: 16, ClassSize: 10,
		StartWeek: 1, EndWeek: 8, Periods: []int{1},
	})
	// idx0 合法，idx1 任务不存在 -> 整体拒绝且报 idx1
	_, err := s.AssignBatch(3, []AssignItem{
		{TaskID: "c1", TeacherID: "t1", Hours: 16},
		{TaskID: "ghost", TeacherID: "t1", Hours: 1},
	})
	if err == nil || err.Code != ErrNotFound || err.Index != 1 {
		t.Fatalf("want NotFound@1, got %v", err)
	}
	if len(s.Snapshot().Shares) != 0 {
		t.Fatal("batch leaked shares")
	}
	// 同批内两项冲突：第二项报 Conflict@1
	mustTeacher(t, s, 4, "t2", "P")
	_, err = s.AssignBatch(5, []AssignItem{
		{TaskID: "c1", TeacherID: "t1", Hours: 8},
		{TaskID: "c1", TeacherID: "t2", Hours: 9}, // 守恒也失败，但冲突先于守恒? 不：守恒在第一遍
	})
	// 守恒校验对任务整体先做 -> 这里应报 ErrHoursConservation（17!=16），下标取最小
	if err == nil || err.Code != ErrHoursConservation {
		t.Fatalf("want conservation, got %v", err)
	}
}

func TestPendingLifecycleAndLazyTimeout(t *testing.T) {
	s := NewService(testConfig())
	mustTeacher(t, s, 1, "t1", "P")
	mustTeacher(t, s, 1, "t2", "P")
	mustTask(t, s, 2, TaskSpec{
		ID: "c1", Semester: "S1", Hours: 16, ClassSize: 10,
		StartWeek: 1, EndWeek: 8, Periods: []int{1},
	})
	ids, err := s.AssignBatch(3, []AssignItem{{"c1", "t1", 16}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids[0] // deadline = 3+10 = 13
	// 恰等期限仍可确认
	if err := s.Confirm(13, id); err != nil {
		t.Fatalf("confirm at exact deadline: %v", err)
	}

	// 第二个任务：超时后惰性释放
	mustTask(t, s, 14, TaskSpec{
		ID: "c2", Semester: "S1", Hours: 8, ClassSize: 10,
		StartWeek: 1, EndWeek: 4, Periods: []int{3},
	})
	ids2, err := s.AssignBatch(15, []AssignItem{{"c2", "t1", 8}})
	if err != nil {
		t.Fatal(err)
	}
	// 用一个新任务 c2b 占用与 c2 完全相同的时段；t2 在 c2 待确认期间会冲突，
	// now=26 > deadline=25 时由后续指派触达惰性释放后立即可用。
	mustTask(t, s, 20, TaskSpec{
		ID: "c2b", Semester: "S1", Hours: 8, ClassSize: 10,
		StartWeek: 1, EndWeek: 4, Periods: []int{3},
	})
	if _, err := s.AssignBatch(20, []AssignItem{{"c2b", "t1", 8}}); err == nil || err.Code != ErrConflict {
		t.Fatalf("want conflict while c2 pending, got %v", err)
	}
	if _, err := s.AssignBatch(26, []AssignItem{{"c2b", "t1", 8}}); err != nil {
		t.Fatalf("lazy release should free slot: %v", err)
	}
	// 已释放的份额不得再确认
	if err := s.Confirm(27, ids2[0]); err == nil || err.Code != ErrState {
		t.Fatalf("confirm released want ErrState, got %v", err)
	}
	// 拒绝即释放
	mustTask(t, s, 28, TaskSpec{
		ID: "c3", Semester: "S1", Hours: 8, ClassSize: 10,
		StartWeek: 1, EndWeek: 4, Periods: []int{4},
	})
	id3, _ := s.AssignBatch(29, []AssignItem{{"c3", "t1", 8}})
	if err := s.Reject(30, id3[0]); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.EffectiveUsed("t1", "S1"); u != 24 { // c1 已确认 16 + c2b 待确认 8
		t.Fatalf("t1 used after reject=%d want 24", u)
	}
}
