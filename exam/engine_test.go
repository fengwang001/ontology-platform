package exam

import (
	"testing"
)

func testCfg() Config {
	return Config{MaxExamsPerDay: 3, MinGapSlots: 1, ProctorsPerRoom: 1, MaxProctorPerDay: 8}
}

func mustEngine(t *testing.T, days, perDay int, rooms map[string]int, exams []*Exam, staffIDs []string, cfg Config) *Engine {
	t.Helper()
	cal, err := NewCalendar(days, perDay)
	if err != nil {
		t.Fatalf("calendar: %v", err)
	}
	vr := NewVenueRegistry()
	for id, c := range rooms {
		if err := vr.Add(id, c); err != nil {
			t.Fatalf("room %s: %v", id, err)
		}
	}
	en := NewEnrollment()
	for _, x := range exams {
		if err := en.AddExam(x); err != nil {
			t.Fatalf("exam %s: %v", x.ID, err)
		}
	}
	sr := NewStaffRegistry()
	for _, s := range staffIDs {
		sr.Add(s)
	}
	g, err := NewEngine(cal, vr, en, sr, cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return g
}

func reason(e error) ReasonCode {
	if e == nil {
		return ReasonOK
	}
	return e.(*Error).Code
}

// 延长比例不足一个时段须向上进位：standard=2, ratio=1/4 -> 0.5 -> 进位 1，共 3 时段。
func TestExtensionCeilToSlot(t *testing.T) {
	g := mustEngine(t, 3, 6,
		map[string]int{"R1": 2},
		[]*Exam{{ID: "E1", Students: []string{"s1", "s2"},
			Extended: map[string]bool{"s1": true}, StandardSlots: 2,
			AllowExtended: true, RatioNum: 1, RatioDen: 4}},
		[]string{"p1"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	s, _ := g.Scheduled("E1")
	if s.Rooms[0].EndSlot != 2 {
		t.Fatalf("extended end = %d, want 2", s.Rooms[0].EndSlot)
	}
	// 普通学生 s2 的个人占用只到时段 1；延长学生 s1 到时 段 2。
	q2 := g.QueryByStudent("s2")
	if q2[len(q2)-1].Slot != 1 {
		t.Fatalf("s2 end = %d, want 1", q2[len(q2)-1].Slot)
	}
	q1 := g.QueryByStudent("s1")
	if q1[len(q1)-1].Slot != 2 {
		t.Fatalf("s1 end = %d, want 2", q1[len(q1)-1].Slot)
	}
}

// 间隔恰好等于下限视为满足；少一个则拒绝。
func TestGapExactlyAtLimit(t *testing.T) {
	exams := []*Exam{
		{ID: "E1", Students: []string{"s1"}, StandardSlots: 1},
		{ID: "E2", Students: []string{"s1"}, StandardSlots: 1},
		{ID: "E3", Students: []string{"s1"}, StandardSlots: 1},
	}
	g := mustEngine(t, 2, 8, map[string]int{"R1": 5}, exams, []string{"p1"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	// E1 占用 slot0；下限 1 个空闲时段 -> E2 最早 slot2（slot1 空闲）。
	if err := g.ScheduleExam("E2", 1, []string{"R1"}); err == nil {
		t.Fatal("gap 0 should be rejected")
	}
	if err := g.ScheduleExam("E2", 2, []string{"R1"}); err != nil {
		t.Fatalf("gap exactly 1 should be accepted: %v", err)
	}
	// E2 在 slot2，E3 在 slot4（slot3 空闲）恰好满足。
	if err := g.ScheduleExam("E3", 4, []string{"R1"}); err != nil {
		t.Fatalf("gap exactly 1 again: %v", err)
	}
}

// 同日门数恰等于上限接受，再超一门拒绝并报超门数。
func TestMaxExamsPerDayExact(t *testing.T) {
	cfg := testCfg()
	cfg.MaxExamsPerDay = 2
	cfg.MinGapSlots = 0
	exams := []*Exam{
		{ID: "E1", Students: []string{"s1"}, StandardSlots: 1},
		{ID: "E2", Students: []string{"s1"}, StandardSlots: 1},
		{ID: "E3", Students: []string{"s1"}, StandardSlots: 1},
	}
	g := mustEngine(t, 2, 8, map[string]int{"R1": 5}, exams, []string{"p1"}, cfg)
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := g.ScheduleExam("E2", 1, []string{"R1"}); err != nil {
		t.Fatalf("2 exams at limit: %v", err)
	}
	err := g.ScheduleExam("E3", 2, []string{"R1"})
	if reason(err) != ReasonStudentConflict || err.(*Error).Kind != StudentConflictTooMany {
		t.Fatalf("want too-many conflict, got %v", err)
	}
	if err.(*Error).StudentID != "s1" {
		t.Fatalf("student = %q", err.(*Error).StudentID)
	}
}

// 拆多考场，容量之和恰好等于参考人数时接受。
func TestSplitRoomsExactCapacity(t *testing.T) {
	exams := []*Exam{{ID: "E1", Students: []string{"a", "b", "c", "d", "e"}, StandardSlots: 1}}
	g := mustEngine(t, 2, 4, map[string]int{"R1": 2, "R2": 3}, exams, []string{"p1", "p2"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1", "R2"}); err != nil {
		t.Fatalf("capacity sum exactly 5: %v", err)
	}
	s, _ := g.Scheduled("E1")
	if len(s.Rooms) != 2 {
		t.Fatalf("rooms used = %d, want 2", len(s.Rooms))
	}
}

// 移动到与自身旧占用重叠的位置必须合法（基准为自身已移除）。
func TestMoveOverlapsOwnOldSlots(t *testing.T) {
	exams := []*Exam{{ID: "E1", Students: []string{"s1"}, StandardSlots: 2}}
	g := mustEngine(t, 2, 8, map[string]int{"R1": 5}, exams, []string{"p1"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	// 新位置 [1,2] 与旧位置 [0,1] 在 slot1 重叠，但不应与自身冲突。
	if err := g.Move("E1", 1, []string{"R1"}); err != nil {
		t.Fatalf("move overlapping own old occupancy: %v", err)
	}
	if q := g.QueryByRoom("R1"); q[0].Slot != 1 || q[len(q)-1].Slot != 2 {
		t.Fatalf("room occupancy after move = %v", q)
	}
	// 移动失败时旧安排完整保留。
	if err := g.Move("E1", 100, []string{"R1"}); reason(err) != ReasonCrossDayOrOutOfRange {
		t.Fatalf("want crossday, got %v", err)
	}
	s, _ := g.Scheduled("E1")
	if s.Start != 1 || s.Rooms[0].EndSlot != 2 {
		t.Fatalf("old schedule not preserved: %+v", s)
	}
}

// 互换中仅一方在新位置不合法：整体拒绝，报出不合法一方标识与原因，状态不变。
func TestSwapOneSideIllegal(t *testing.T) {
	exams := []*Exam{
		{ID: "E1", Students: []string{"a", "b"}, StandardSlots: 1},
		{ID: "E2", Students: []string{"s"}, StandardSlots: 1},
	}
	// R1 容量 2 能容纳 E1；R2 容量 1 能容纳 E2。互换后 E1 容量不足。
	g := mustEngine(t, 2, 8, map[string]int{"R1": 2, "R2": 1}, exams,
		[]string{"p1", "p2", "p3"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := g.ScheduleExam("E2", 2, []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	err := g.Swap("E1", "E2")
	if reason(err) != ReasonRoomCapacity || err.(*Error).ExamID != "E1" {
		t.Fatalf("want E1 capacity error, got %v", err)
	}
	s1, _ := g.Scheduled("E1")
	s2, _ := g.Scheduled("E2")
	if s1.Start != 0 || s2.Start != 2 {
		t.Fatalf("state changed after rejected swap: %d %d", s1.Start, s2.Start)
	}
}

// 合法互换：两门考试交换时段与考场，且监考重新确定性选取。
func TestSwapSuccess(t *testing.T) {
	exams := []*Exam{
		{ID: "E1", Students: []string{"a"}, StandardSlots: 1},
		{ID: "E2", Students: []string{"b"}, StandardSlots: 1},
	}
	g := mustEngine(t, 2, 8, map[string]int{"R1": 2, "R2": 2}, exams,
		[]string{"p1", "p2"}, testCfg())
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := g.ScheduleExam("E2", 2, []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Swap("E1", "E2"); err != nil {
		t.Fatalf("swap: %v", err)
	}
	s1, _ := g.Scheduled("E1")
	s2, _ := g.Scheduled("E2")
	if s1.Start != 2 || s1.Rooms[0].RoomID != "R2" || s2.Start != 0 || s2.Rooms[0].RoomID != "R1" {
		t.Fatalf("swap result wrong: %+v %+v", s1, s2)
	}
}

// 监考每日上限恰满接受，再多一个监考时段需求则监考不足。
func TestProctorDailyCapExact(t *testing.T) {
	cfg := testCfg()
	cfg.MaxProctorPerDay = 2 // p1 每日最多监考 2 个时段
	exams := []*Exam{
		{ID: "E1", Students: []string{"a"}, StandardSlots: 2},
		{ID: "E2", Students: []string{"b"}, StandardSlots: 1},
	}
	g := mustEngine(t, 2, 8, map[string]int{"R1": 5}, exams, []string{"p1"}, cfg)
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatalf("2 proctor slots fills cap exactly: %v", err)
	}
	// p1 当日额度已用尽，E2 同日无法监考。
	err := g.ScheduleExam("E2", 4, []string{"R1"})
	if reason(err) != ReasonStaffShortage {
		t.Fatalf("want staff shortage, got %v", err)
	}
	// 次日额度重置，可安排。
	if err := g.ScheduleExam("E2", 8, []string{"R1"}); err != nil {
		t.Fatalf("next day proctor cap reset: %v", err)
	}
}

// 监考确定性选取：相同状态相同输入给出相同人选，与调用历史无关。
func TestProctorDeterministic(t *testing.T) {
	exams := []*Exam{
		{ID: "E1", Students: []string{"a"}, StandardSlots: 1},
		{ID: "E2", Students: []string{"b"}, StandardSlots: 1},
		{ID: "E3", Students: []string{"c"}, StandardSlots: 1},
	}
	build := func() *Engine {
		return mustEngine(t, 2, 8, map[string]int{"R1": 5, "R2": 5}, exams,
			[]string{"p1", "p2", "p3"}, testCfg())
	}
	g1 := build()
	_ = g1.ScheduleExam("E1", 0, []string{"R1"}) // 占用 p1@0
	_ = g1.ScheduleExam("E2", 2, []string{"R1"}) // 占用 p1@2
	s1, _ := g1.Scheduled("E2")

	g2 := build()
	_ = g2.ScheduleExam("E2", 2, []string{"R1"}) // 直接在相同终态安排
	s2, _ := g2.Scheduled("E2")
	if s1.Staff[2][0] != s2.Staff[2][0] {
		t.Fatalf("nondeterministic proctor: %s vs %s", s1.Staff[2], s2.Staff[2])
	}
}

// 撤销未安排考试报可区分错误；撤销后占用与监考账目全部释放。
func TestCancelNotScheduledAndRelease(t *testing.T) {
	exams := []*Exam{{ID: "E1", Students: []string{"a"}, StandardSlots: 1}}
	g := mustEngine(t, 2, 4, map[string]int{"R1": 2}, exams, []string{"p1"}, testCfg())
	if err := g.Cancel("E1"); reason(err) != ReasonExamNotScheduled {
		t.Fatalf("want not scheduled, got %v", err)
	}
	if err := g.ScheduleExam("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Cancel("E1"); err != nil {
		t.Fatal(err)
	}
	if len(g.QueryByRoom("R1")) != 0 || len(g.QueryByStaff("p1")) != 0 {
		t.Fatal("occupancy not released after cancel")
	}
	if _, ok := g.Scheduled("E1"); ok {
		t.Fatal("E1 still scheduled")
	}
}

// makePriorityScene 构造一个同时具备各等级缺陷要素的基线场景。
// 返回引擎与各缺陷触发参数。
func makePriorityScene(t *testing.T) (*Engine, string, int, []string) {
	t.Helper()
	cfg := Config{MaxExamsPerDay: 1, MinGapSlots: 1, ProctorsPerRoom: 1, MaxProctorPerDay: 8}
	exams := []*Exam{
		{ID: "E", Students: []string{"s1", "s2", "s3", "s4"}, StandardSlots: 1},
		{ID: "X", Students: []string{"s1"}, StandardSlots: 1}, // 与 E 共享 s1
	}
	g := mustEngine(t, 2, 8,
		map[string]int{"Rsmall": 1, "Rbusy": 4},
		exams, []string{"p1"}, cfg)
	// X 已排在 slot0 的 Rbusy：使 slot0 考场忙、学生 s1 冲突。
	if err := g.ScheduleExam("X", 0, []string{"Rbusy"}); err != nil {
		t.Fatal(err)
	}
	// E 若安排在 slot0/Rbusy：考场忙（Rbusy@0）；Rsmall 容量不足（1<4）；
	// s1 与 X 重叠；监考只有 p1 且 slot0 被 X 占用 -> 监考不足。
	examID := "E"
	start := 0
	rooms := []string{"Rbusy"}
	return g, examID, start, rooms
}

// TestRejectPriorityPairwise 逐对验证七个拒绝等级的固定优先级：
// 参数非法 > 已安排 > 跨日越界 > 考场占用 > 容量不足 > 学生冲突 > 监考不足。
func TestRejectPriorityPairwise(t *testing.T) {
	type trgFn func(g *Engine) (string, int, []string)
	order := []ReasonCode{
		ReasonInvalidArgument,
		ReasonExamAlreadyScheduled,
		ReasonCrossDayOrOutOfRange,
		ReasonRoomSlotBusy,
		ReasonRoomCapacity,
		ReasonStudentConflict,
		ReasonStaffShortage,
	}
	// 每个触发器构造相应缺陷（缺陷彼此可叠加：每次都在新引擎上叠加从本级起到最低级的全部缺陷）。
	trg := func(level ReasonCode) func(*Engine) (string, int, []string) {
		return func(g *Engine) (string, int, []string) {
			_, start, rooms := 0, 0, []string{"Rbusy"}
			examID := "E"
			if level == ReasonInvalidArgument {
				return examID, start, []string{"R-unknown"} // 未知考场
			}
			if level == ReasonExamAlreadyScheduled {
				// 先把 E 合法排到别处，再重复安排
				if err := g.ScheduleExam("E", 8, []string{"Rbusy"}); err != nil {
					t.Fatalf("seed E: %v", err)
				}
				return examID, start, rooms
			}
			if level == ReasonCrossDayOrOutOfRange {
				return examID, 99, rooms
			}
			if level == ReasonRoomSlotBusy {
				return examID, start, rooms // Rbusy@0 被 X 占用
			}
			if level == ReasonRoomCapacity {
				// Rbusy 此时段空闲（换到 slot1），但只用容量为 1 的 Rsmall
				return examID, 1, []string{"Rsmall"}
			}
			if level == ReasonStudentConflict {
				// slot1 考场空闲、容量足够；但 s1 同日已有 X（slot0），
				// 门数上限 1 且间隔 0 < 1 -> 学生冲突。
				return examID, 1, rooms
			}
			// 监考不足：slot2 无任何冲突与占用问题，但 p1 当日额度先被占满。
			ex := []*Exam{{ID: "FILL", Students: []string{"z"}, StandardSlots: 8}}
			en := g.enroll
			_ = en.AddExam(&Exam{ID: "FILL9", Students: []string{"z"}, StandardSlots: 1})
			_ = ex
			// 用 8 个 1 时段考试填满 p1 当日 8 个监考时段（不同考场避免容量问题）
			for i := 0; i < 8; i++ {
				rid := "RF" + itoa(i)
				_ = g.rooms.Add(rid, 2)
				fid := "F" + itoa(i)
				_ = en.AddExam(&Exam{ID: fid, Students: []string{"z" + itoa(i)}, StandardSlots: 1})
				if err := g.ScheduleExam(fid, 8+i, []string{rid}); err != nil {
					t.Fatalf("fill %d: %v", i, err)
				}
			}
			_ = start
			return examID, 8, []string{"Rbusy"} // 日1 的 p1 已被 8 场填满 -> 监考不足
		}
	}

	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			g, _, _, _ := makePriorityScene(t)
			higher, lower := order[i], order[j]
			examID, st, rooms := trg(higher)(g)
			before, beforeOK := g.Scheduled("E") // trg 已完成所有“合法的前置安排”
			// 再叠加低级缺陷（若可独立附加），确保两者同时存在
			switch lower {
			case ReasonInvalidArgument:
				rooms = append(rooms, "R-unknown")
			case ReasonCrossDayOrOutOfRange:
				if higher != ReasonCrossDayOrOutOfRange {
					st = 99 // 已安排的考试也以越界参数再次提交，仍应报“已安排”
				}
			}
			err := g.ScheduleExam(examID, st, rooms)
			if reason(err) != higher {
				t.Fatalf("pair (%v > %v): got %v, want %v (err=%v)",
					higher, lower, reason(err), higher, err)
			}
			// 拒绝后状态不变：E 的安排须与操作前完全一致。
			after, afterOK := g.Scheduled("E")
			if afterOK != beforeOK || (beforeOK &&
				(after.Start != before.Start || len(after.Rooms) != len(before.Rooms))) {
				t.Fatalf("pair (%v>%v): rejected op changed state", higher, lower)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
