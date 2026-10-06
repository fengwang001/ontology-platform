package scheduler

import (
	"errors"
	"sync"
	"testing"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	rooms := []Room{{ID: "R1", Capacity: 30}, {ID: "R2", Capacity: 20}, {ID: "R3", Capacity: 10}}
	proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}, {ID: "P4"}, {ID: "P5"}}
	eng, err := NewEngine(Config{
		SlotsPerDay:           8,
		Days:                  3,
		MaxExamsPerStudent:    2,
		MinGapSlots:           1,
		ExtensionRatio:        0.5,
		MaxProctorSlotsPerDay: 4,
	}, rooms, proctors)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

func addExam(t *testing.T, e *Engine, ex Exam) {
	t.Helper()
	if err := e.AddExam(ex); err != nil {
		t.Fatalf("AddExam %s: %v", ex.ID, err)
	}
}

func errCode(err error) ReasonCode {
	if err == nil {
		return ""
	}
	return err.(*SchedError).Code
}

// 加时比例换算的边界：标准 3 段、比例 0.5 -> 延长 1.5 向上进位为 2，总占 5 段。
func TestExtensionCeilBoundary(t *testing.T) {
	e := testEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, ExtendedStudents: []string{"s1"}, StandardSlots: 3, AllowExtension: true})
	p, err := e.Schedule("E1", 0, []string{"R1"})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if p.StartSlot != 0 || p.EndSlot != 4 {
		t.Fatalf("want [0,4], got [%d,%d]", p.StartSlot, p.EndSlot)
	}
	// 标准 2 段 * 0.5 = 恰好 1 段，不应进位。
	addExam(t, e, Exam{ID: "E2", Students: []string{"s2"}, ExtendedStudents: []string{"s2"}, StandardSlots: 2, AllowExtension: true})
	p2, err := e.Schedule("E2", 8, []string{"R2"})
	if err != nil || p2.EndSlot != 10 {
		t.Fatalf("want [8,10], got %+v err=%v", p2, err)
	}
}

// 不允许加时 / 无延长学生时，只占标准时段。
func TestNoExtension(t *testing.T) {
	e := testEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 2, AllowExtension: true})
	p, err := e.Schedule("E1", 0, []string{"R1"})
	if err != nil || p.EndSlot != 1 {
		t.Fatalf("got %+v err=%v", p, err)
	}
}

// 间隔恰等于下限（1）应满足；少于下限应拒绝。
func TestGapBoundary(t *testing.T) {
	rooms := []Room{{ID: "R1", Capacity: 30}, {ID: "R2", Capacity: 20}, {ID: "R3", Capacity: 10}}
	proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}
	e, err := NewEngine(Config{SlotsPerDay: 8, Days: 3, MaxExamsPerStudent: 5, MinGapSlots: 1, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 8}, rooms, proctors)
	if err != nil {
		t.Fatal(err)
	}
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 1})
	addExam(t, e, Exam{ID: "E2", Students: []string{"s1"}, StandardSlots: 1})
	addExam(t, e, Exam{ID: "E3", Students: []string{"s1"}, StandardSlots: 1})
	if _, err = e.Schedule("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	// E1 占 slot0；E2 放 slot2，中间恰好 1 个空闲段 slot1，合法。
	if _, err = e.Schedule("E2", 2, []string{"R2"}); err != nil {
		t.Fatalf("gap equal min: %v", err)
	}
	// E3 放 slot3，与 E2 相邻（gap=0），应报间隔不足。
	_, err = e.Schedule("E3", 3, []string{"R3"})
	if errCode(err) != ReasonStudentConflict {
		t.Fatalf("want student conflict, got %v", err)
	}
	se := err.(*SchedError)
	if se.Student != "s1" || se.Category != ConflictGapTooSmall {
		t.Fatalf("want s1/gap, got %s/%s", se.Student, se.Category)
	}
}

// 同日门数恰等于上限合法，再加一门拒绝（超门数）。
func TestPerDayCountBoundary(t *testing.T) {
	e := testEngine(t)
	slots := []int{0, 3} // 间隔 1 段空闲段，满足 MinGapSlots=1
	for i, id := range []string{"E1", "E2"} {
		addExam(t, e, Exam{ID: id, Students: []string{"s1"}, StandardSlots: 1})
		slot := slots[i]
		if _, err := e.Schedule(id, slot, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
	}
	addExam(t, e, Exam{ID: "E3", Students: []string{"s1"}, StandardSlots: 1})
	_, err := e.Schedule("E3", 6, []string{"R1"})
	if errCode(err) != ReasonStudentConflict || err.(*SchedError).Category != ConflictOverCount {
		t.Fatalf("want over_count, got %v", err)
	}
	// 次日安排不受当日计数影响。
	addExam(t, e, Exam{ID: "E4", Students: []string{"s1"}, StandardSlots: 1})
	if _, err := e.Schedule("E4", 8, []string{"R1"}); err != nil {
		t.Fatalf("next day should be fine: %v", err)
	}
}

// 多考场容量之和恰等于人数时合法；少 1 人时拒绝且错误可区分。
func TestMultiRoomCapacityExact(t *testing.T) {
	e := testEngine(t)
	// R2(20)+R3(10)=30 恰等于 30 人。
	students := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		students = append(students, "s"+pad2(i))
	}
	addExam(t, e, Exam{ID: "E1", Students: students, StandardSlots: 1})
	p, err := e.Schedule("E1", 0, []string{"R2", "R3"})
	if err != nil {
		t.Fatalf("exact capacity: %v", err)
	}
	totalUsed := 0
	for _, ra := range p.Rooms {
		if ra.CapacityUsed < 0 || ra.CapacityUsed > 30 {
			t.Fatalf("bad used %d", ra.CapacityUsed)
		}
		totalUsed += ra.CapacityUsed
	}
	if totalUsed != 30 {
		t.Fatalf("seated=%d want 30", totalUsed)
	}
	students29 := students[:29]
	addExam(t, e, Exam{ID: "E2", Students: students29, StandardSlots: 1})
	// R3(10)+? 只用 R3 不足。
	_, err = e.Schedule("E2", 1, []string{"R3"})
	if errCode(err) != ReasonCapacityInsufficient {
		t.Fatalf("want capacity insufficient, got %v", err)
	}
}

func pad2(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return "0" + string(digits[i])
	}
	return string(digits[i/10]) + string(digits[i%10])
}

// 移动到与自身旧占用重叠的位置应合法（不与自己冲突）。
func TestMoveOverlapsOwnOldSlot(t *testing.T) {
	e := testEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 2})
	p, err := e.Schedule("E1", 0, []string{"R1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.EndSlot != 1 {
		t.Fatalf("bad init %+v", p)
	}
	// 旧占 [0,1]，新占 [1,2]，与自身重叠但与别人不冲突。
	p2, err := e.Move("E1", 1, []string{"R1"})
	if err != nil {
		t.Fatalf("move onto own old slot: %v", err)
	}
	if p2.StartSlot != 1 || p2.EndSlot != 2 {
		t.Fatalf("bad moved %+v", p2)
	}
	if got := e.StudentSlots("s1"); len(got) != 2 || got[0].Slot != 1 {
		t.Fatalf("stale occupancy after move: %+v", got)
	}
}

// 互换：一方合法、一方与第三方冲突 -> 整体拒绝，报错指向不合法一方。
func TestSwapOneSideInvalid(t *testing.T) {
	e := testEngine(t)
	addExam(t, e, Exam{ID: "A", Students: []string{"s1"}, StandardSlots: 1})
	addExam(t, e, Exam{ID: "B", Students: []string{"s2"}, StandardSlots: 1})
	addExam(t, e, Exam{ID: "C", Students: []string{"s1", "s3"}, StandardSlots: 1})
	if _, err := e.Schedule("A", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Schedule("B", 2, []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	// C 与 A 的学生 s1 相同但放在 slot3（间隔 2 段，合法），占用 R3。
	if _, err := e.Schedule("C", 5, []string{"R3"}); err != nil {
		t.Fatal(err)
	}
	// 当前 A=(0,R1), B=(2,R2), C=(5,R3)。
	// 互换后 A->(2,R2) 合法；B->(0,R1)：s2 无冲突，合法。
	// 构造“仅一方不合法”：先把 C 放在与 A 新位置冲突处不行（会影响现状），
	// 改为让 B 的学生与 slot0 的第三方冲突：新增 D 的学生含 s2，占 slot0 的 R?。
	// slot0 此时 R1 被 A 占用、R2/R3 空；D=(0,R3) 含 s2，现状合法
	//（s2 的 B 在 slot2，间隔 1 段 slot1，恰满足下限）。
	addExam(t, e, Exam{ID: "D", Students: []string{"s2", "s4"}, StandardSlots: 1})
	if _, err := e.Schedule("D", 0, []string{"R3"}); err != nil {
		t.Fatal(err)
	}
	// 互换后：A->(2,R2)（s1 在 slot2，与 slot5 的 C 间隔 2 段，合法）；
	// B->(0,R1)：s2 在 slot0 与 D(s2, slot0) 完全重叠 -> 仅 B 方不合法。
	_, _, err := e.Swap("A", "B")
	if errCode(err) != ReasonStudentConflict {
		t.Fatalf("want student conflict, got %v", err)
	}
	se := err.(*SchedError)
	if se.ExamID != "B" {
		t.Fatalf("want offending exam B, got %q", se.ExamID)
	}
	if se.Student != "s2" || se.Category != ConflictOverlap {
		t.Fatalf("want s2/overlap, got %s/%s", se.Student, se.Category)
	}
	// 原子性：A、B 旧安排必须完整保留。
	if pa, ok := e.GetPlacement("A"); !ok || pa.StartSlot != 0 {
		t.Fatalf("A not preserved: %+v", pa)
	}
	if pb, ok := e.GetPlacement("B"); !ok || pb.StartSlot != 2 {
		t.Fatalf("B not preserved: %+v", pb)
	}

	// 合法互换：把 D 移走后，互换应成功并交换时段与考场。
	if err := e.Cancel("D"); err != nil {
		t.Fatal(err)
	}
	pa, pb, err := e.Swap("A", "B")
	if err != nil {
		t.Fatalf("valid swap: %v", err)
	}
	if pa.StartSlot != 2 || pa.Rooms[0].RoomID != "R2" {
		t.Fatalf("A after swap: %+v", pa)
	}
	if pb.StartSlot != 0 || pb.Rooms[0].RoomID != "R1" {
		t.Fatalf("B after swap: %+v", pb)
	}
}

// 监考每日上限恰满：上限 4，一门考试用同一监考连占 4 段合法，第 5 段缺人。
func TestProctorDailyCapExact(t *testing.T) {
	rooms := []Room{{ID: "R1", Capacity: 5}, {ID: "R2", Capacity: 5}, {ID: "R3", Capacity: 5}, {ID: "R4", Capacity: 5}}
	proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}
	eng, err := NewEngine(Config{SlotsPerDay: 8, Days: 2, MaxExamsPerStudent: 5, MinGapSlots: 0, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 4}, rooms, proctors)
	if err != nil {
		t.Fatal(err)
	}
	addExam(t, eng, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 4})
	p, err := eng.Schedule("E1", 0, []string{"R1"})
	if err != nil {
		t.Fatalf("4 slots exactly at cap: %v", err)
	}
	for slot := 0; slot < 4; slot++ {
		if p.Proctors[slot]["R1"] != "P1" {
			t.Fatalf("slot %d proctor = %q want P1", slot, p.Proctors[slot]["R1"])
		}
	}
	// 恰满当日上限：另一门考试跨 slot0..3 同时占用 R2、R3，需要 P2、P3 各 4 段。
	addExam(t, eng, Exam{ID: "E2", Students: []string{"s2", "s3"}, StandardSlots: 4})
	if _, err := eng.Schedule("E2", 0, []string{"R2", "R3"}); err != nil {
		t.Fatalf("P2/P3 fill cap exactly: %v", err)
	}
	// slot2 三个岗位时三名监考均在同时段已占用（且当日都已满 4），第 4 个岗位无人。
	addExam(t, eng, Exam{ID: "E3", Students: []string{"s4"}, StandardSlots: 1})
	_, err = eng.Schedule("E3", 2, []string{"R4"})
	if errCode(err) != ReasonProctorInsufficient {
		t.Fatalf("want proctor insufficient, got %v", err)
	}
	// 次日人力重置，应可安排。
	addExam(t, eng, Exam{ID: "E4", Students: []string{"s5"}, StandardSlots: 1})
	if _, err := eng.Schedule("E4", 8, []string{"R1"}); err != nil {
		t.Fatalf("next day proctors reset: %v", err)
	}
}

// 构造一个含 4 考场、4 监考的引擎，用于优先级与并发测试。
func priorityEngine(t *testing.T) *Engine {
	t.Helper()
	rooms := []Room{{ID: "R1", Capacity: 2}, {ID: "R2", Capacity: 2}, {ID: "R3", Capacity: 2}, {ID: "R4", Capacity: 2}}
	proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}, {ID: "P4"}}
	eng, err := NewEngine(Config{SlotsPerDay: 8, Days: 3, MaxExamsPerStudent: 3, MinGapSlots: 1, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 8}, rooms, proctors)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func schedCode(t *testing.T, e *Engine, exam string, slot int, rooms []string) ReasonCode {
	t.Helper()
	_, err := e.Schedule(exam, slot, rooms)
	return errCode(err)
}

// 逐对验证拒绝优先级：
// invalid > already_placed > cross_day > room_busy > capacity > student > proctor。
// 每个子用例都构造同时命中相邻两级的输入，断言只暴露较高优先级。
func TestRejectPriorityPairwise(t *testing.T) {
	// 1) invalid_param > exam_already_placed：已安排考试 + 未知考场。
	{
		e := priorityEngine(t)
		addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 1})
		if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
		if got := schedCode(t, e, "E1", 1, []string{"NOPE"}); got != ReasonInvalidParam {
			t.Fatalf("invalid vs placed: got %s", got)
		}
	}
	// 2) already_placed > cross_day：已安排考试排到跨日。
	{
		e := priorityEngine(t)
		addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 2})
		if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
		if got := schedCode(t, e, "E1", 7, []string{"R2"}); got != ReasonExamAlreadyPlaced {
			t.Fatalf("placed vs crossday: got %s", got)
		}
	}
	// 3) cross_day > room_busy：跨日区间同时落在被占用考场。
	{
		e := priorityEngine(t)
		addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 2})
		addExam(t, e, Exam{ID: "E2", Students: []string{"s2"}, StandardSlots: 2})
		if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
		if got := schedCode(t, e, "E2", 7, []string{"R1"}); got != ReasonCrossDayOrOutOfRange {
			t.Fatalf("crossday vs busy: got %s", got)
		}
		// 越界（负号 / 超尾）同属一级。
		if got := schedCode(t, e, "E2", -1, []string{"R1"}); got != ReasonCrossDayOrOutOfRange {
			t.Fatalf("negative slot: got %s", got)
		}
	}
	// 4) room_busy > capacity：考场时段被占，且换更小的考场容量不足。
	{
		e := priorityEngine(t)
		students := []string{"s1", "s2", "s3"} // 3 人，任何单考场容量 2 都不足
		addExam(t, e, Exam{ID: "E1", Students: students, StandardSlots: 1})
		addExam(t, e, Exam{ID: "E2", Students: []string{"s9"}, StandardSlots: 1})
		if _, err := e.Schedule("E2", 0, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
		if got := schedCode(t, e, "E1", 0, []string{"R1"}); got != ReasonRoomSlotBusy {
			t.Fatalf("busy vs capacity: got %s", got)
		}
	}
	// 5) capacity > student：容量不足的考场组合同时造成学生重叠。
	{
		e := priorityEngine(t)
		addExam(t, e, Exam{ID: "E1", Students: []string{"s1", "s2", "s3"}, StandardSlots: 1})
		addExam(t, e, Exam{ID: "E2", Students: []string{"s1", "s2"}, StandardSlots: 1})
		// E2 占 R1+R2（容量和 4，学生 s1/s2），slot0。
		if _, err := e.Schedule("E2", 0, []string{"R1", "R2"}); err != nil {
			t.Fatal(err)
		}
		// E1 三个人选空闲的 R3+R4：容量和 4 其实足够，改为只用 R3(2) 制造容量不足；
		// R3 该时段空闲，故无考场占用，但 E1 含 s1 与 E2 学生重叠。
		if got := schedCode(t, e, "E1", 0, []string{"R3"}); got != ReasonCapacityInsufficient {
			t.Fatalf("capacity vs student: got %s", got)
		}
	}
	// 6) student > proctor：学生冲突时即便监考也不够，仍报学生冲突。
	{
		rooms := []Room{{ID: "R1", Capacity: 5}, {ID: "R2", Capacity: 5}, {ID: "R3", Capacity: 5}, {ID: "R4", Capacity: 5}}
		proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}
		eng, err := NewEngine(Config{SlotsPerDay: 8, Days: 2, MaxExamsPerStudent: 3, MinGapSlots: 1, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 8}, rooms, proctors)
		if err != nil {
			t.Fatal(err)
		}
		// 背景：slot0 的 R1/R2/R3 三个岗位恰好占满三名监考（合法）。
		addExam(t, eng, Exam{ID: "E1", Students: []string{"x", "y", "z"}, StandardSlots: 1})
		if _, err := eng.Schedule("E1", 0, []string{"R1", "R2", "R3"}); err != nil {
			t.Fatal(err)
		}
		addExam(t, eng, Exam{ID: "E2", Students: []string{"s1"}, StandardSlots: 1})
		if _, err := eng.Schedule("E2", 2, []string{"R1"}); err != nil {
			t.Fatal(err)
		}
		// E3 含 s1 且放 slot2：与 E2(s1, slot2) 学生重叠；同时 slot2 要新增
		// R4 岗位，而该日三名监考在 slot0 各占 1、在 slot2 只有 P1 忙——
		// 为确保监考也确实不足，让 E3 拆两个空闲考场 R3/R4 于 slot2，
		// 需两名监考；slot2 已忙的有 P1(R1)，剩 P2、P3 恰好两人仍够，
		// 故再放背景 E4 占满 slot2 的 P2、P3。
		addExam(t, eng, Exam{ID: "E4", Students: []string{"a", "b"}, StandardSlots: 1})
		if _, err := eng.Schedule("E4", 2, []string{"R2", "R3"}); err != nil {
			t.Fatal(err)
		}
		addExam(t, eng, Exam{ID: "E3", Students: []string{"s1", "s9"}, StandardSlots: 1})
		_, err = eng.Schedule("E3", 2, []string{"R4"})
		if errCode(err) != ReasonStudentConflict {
			t.Fatalf("student vs proctor: got %v", err)
		}
	}
}

// 学生冲突三类别同时成立时只报重叠；同类取最小学生。
func TestStudentConflictPrecedenceAndMinID(t *testing.T) {
	e := priorityEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"zzz"}, StandardSlots: 2})
	if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	addExam(t, e, Exam{ID: "E2", Students: []string{"aaa", "zzz"}, StandardSlots: 1})
	// aaa 新学生：slot0 与 E1(zzz) 无关；zzz：与 E1 重叠。
	_, err := e.Schedule("E2", 0, []string{"R2"})
	var se *SchedError
	if !errors.As(err, &se) {
		t.Fatalf("want SchedError, got %v", err)
	}
	if se.Category != ConflictOverlap || se.Student != "zzz" {
		t.Fatalf("want zzz/overlap, got %s/%s", se.Student, se.Category)
	}
}

// 撤销：释放占用后可重新安排；撤销未安排考试报可区分错误。
func TestCancel(t *testing.T) {
	e := priorityEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 1})
	if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Cancel("NOPE"); errCode(err) != ReasonExamNotPlaced {
		t.Fatalf("cancel unscheduled: %v", err)
	}
	if err := e.Cancel("E1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.GetPlacement("E1"); ok {
		t.Fatal("E1 should be unscheduled")
	}
	if got := e.StudentSlots("s1"); len(got) != 0 {
		t.Fatalf("student occupancy not released: %+v", got)
	}
	if got, _ := e.RoomSlots("R1"); len(got) != 0 {
		t.Fatalf("room occupancy not released: %+v", got)
	}
	if got := e.ProctorSlots("P1"); len(got) != 0 {
		t.Fatalf("proctor occupancy not released: %+v", got)
	}
	// 释放后同一时段可再次排同一门课。
	if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
		t.Fatalf("reschedule after cancel: %v", err)
	}
}

// 查询结果按时段升序；移动未安排考试报 exam_not_placed。
func TestQueriesSortedAndMoveErrors(t *testing.T) {
	e := priorityEngine(t)
	addExam(t, e, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 1})
	addExam(t, e, Exam{ID: "E2", Students: []string{"s1"}, StandardSlots: 1})
	if _, err := e.Schedule("E1", 0, []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Schedule("E2", 3, []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	got := e.StudentSlots("s1")
	if len(got) != 2 || got[0].Slot != 0 || got[1].Slot != 3 {
		t.Fatalf("student slots order: %+v", got)
	}
	if _, err := e.Move("NOPE", 5, []string{"R1"}); errCode(err) != ReasonInvalidParam {
		t.Fatalf("move unscheduled: %v", err)
	}
	addExam(t, e, Exam{ID: "E3", Students: []string{"s9"}, StandardSlots: 1})
	if _, err := e.Move("E3", 5, []string{"R1"}); errCode(err) != ReasonExamNotPlaced {
		t.Fatalf("move registered-but-unplaced: %v", err)
	}
	if _, err := e.RoomSlots("NOPE"); errCode(err) != ReasonInvalidParam {
		t.Fatalf("query unknown room: %v", err)
	}
}

// 确定性：相同状态、相同输入（经不同调用历史）得到相同监考人选。
func TestProctorSelectionDeterminism(t *testing.T) {
	build := func() (*Engine, func()) {
		rooms := []Room{{ID: "R1", Capacity: 5}, {ID: "R2", Capacity: 5}}
		proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}
		eng, err := NewEngine(Config{SlotsPerDay: 8, Days: 2, MaxExamsPerStudent: 5, MinGapSlots: 0, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 8}, rooms, proctors)
		if err != nil {
			t.Fatal(err)
		}
		return eng, func() {}
	}
	pick := func(eng *Engine) string {
		addExam(t, eng, Exam{ID: "E1", Students: []string{"s1"}, StandardSlots: 1})
		p, err := eng.Schedule("E1", 0, []string{"R1"})
		if err != nil {
			t.Fatal(err)
		}
		return p.Proctors[0]["R1"]
	}
	engA, _ := build()
	first := pick(engA)
	// 另一份状态：先排若干别的考试再撤销（改变调用历史），再排同样输入。
	engB, _ := build()
	addExam(t, engB, Exam{ID: "X", Students: []string{"sx"}, StandardSlots: 1})
	if _, err := engB.Schedule("X", 5, []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	if err := engB.Cancel("X"); err != nil {
		t.Fatal(err)
	}
	second := pick(engB)
	if first != second || first != "P1" {
		t.Fatalf("nondeterministic: %q vs %q", first, second)
	}
}

// 并发：大量并发安排同一稀缺考场，接受数恒等于容量允许的安排数，
// 且最终全部已接受安排同时满足约束（以查询不出现双人同时段为校验）。
func TestConcurrentSchedule(t *testing.T) {
	rooms := []Room{{ID: "R1", Capacity: 50}}
	proctors := []Proctor{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}, {ID: "P4"}, {ID: "P5"}, {ID: "P6"}, {ID: "P7"}, {ID: "P8"}}
	eng, err := NewEngine(Config{SlotsPerDay: 8, Days: 4, MaxExamsPerStudent: 8, MinGapSlots: 0, ExtensionRatio: 0.5, MaxProctorSlotsPerDay: 8}, rooms, proctors)
	if err != nil {
		t.Fatal(err)
	}
	const n = 40
	for i := 0; i < n; i++ {
		addExam(t, eng, Exam{ID: "E" + itoa2(i), Students: []string{"s" + itoa2(i)}, StandardSlots: 1})
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := eng.Schedule("E"+itoa2(i), i%8, []string{"R1"})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	// 8 个时段，每个时段考场只容一门，监考 8 人/时段足够 -> 至多 8 门被接受。
	if accepted != 8 {
		t.Fatalf("accepted=%d want 8", accepted)
	}
	// 每个时段恰有一门占用。
	for slot := 0; slot < 8; slot++ {
		occ, _ := eng.RoomSlots("R1")
		count := 0
		for _, o := range occ {
			if o.Slot == slot {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("slot %d occupancy %d want 1", slot, count)
		}
	}
}

func itoa2(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
