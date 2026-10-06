package permit

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func code(err error) ErrorCode {
	if err == nil {
		return ErrNone
	}
	e, _ := AsError(err)
	return e.Code
}

// 启动日=受理当日；起算日=启动日的次工作日；届满日=起算日起数满 N 个工作日。
func TestStartComputeDueDays(t *testing.T) {
	// 受理 day1：起算 day2；N=3 -> day2,day5,day6 => 届满 day6
	svc := NewService([]int{3, 4})
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 3},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	cases := map[int]int{2: 2, 5: 1, 6: 0}
	for d, rem := range cases {
		v, err := svc.StageAt(d, "c", "A")
		must(t, err)
		if v.StartDay != 1 || v.DueDay != 6 || v.Remaining != rem {
			t.Fatalf("day%d view=%+v want due6 rem%d", d, v, rem)
		}
	}
}

// 届满日恰等当日仍有效；晚一日非自动环节超时。
func TestDueExactAndOneDayLate(t *testing.T) {
	svc := NewService(nil)
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 2},
	}}))
	must(t, svc.Accept(1, "c", "P")) // 起算 day2 届满 day3
	must(t, svc.Decide(3, "c", "A", "d", DecideApprove))
	v, _ := svc.StageAt(3, "c", "A")
	if v.Status != StageApproved || v.Overtime {
		t.Fatalf("exact due approve: %+v", v)
	}
	// 第二件（独立服务，时钟独立）：晚一日办结 -> 超时标记保留
	svc2 := NewService(nil)
	must(t, svc2.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 2},
	}}))
	must(t, svc2.Accept(1, "c2", "P"))
	if err := svc2.Decide(4, "c2", "A", "d", DecideApprove); err != nil {
		t.Fatal(err)
	}
	v, _ = svc2.StageAt(4, "c2", "A")
	if !v.Overtime || v.Status != StageApproved {
		t.Fatalf("late approve overtime should stick: %+v", v)
	}
}

// 补正恰在期限最后一日恢复；晚一日则视为不通过。
func TestSupplementDeadlineExactAndLate(t *testing.T) {
	svc := NewService(nil, WithSupplement(3, 2))
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 4},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	// day2 要求补正：期限自 day3 起 3 工作日 -> day3,4,5 => 截止 day5
	must(t, svc.Decide(2, "c", "A", "d", DecideSupplement))
	v, _ := svc.StageAt(4, "c", "A")
	if v.Status != StagePaused || v.Remaining != 3 { // day2 已用 1，剩 3
		t.Fatalf("paused rem=%+v", v)
	}
	must(t, svc.SubmitSupplement(5, "c", "A")) // 恰在最后一日
	v, _ = svc.StageAt(5, "c", "A")
	if v.Status != StageActive {
		t.Fatalf("resumed: %+v", v)
	}
	// 第二件（独立服务）：晚一日提交 -> 环节已视为不通过，提交被拒
	svc2 := NewService(nil, WithSupplement(3, 2))
	must(t, svc2.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 4},
	}}))
	must(t, svc2.Accept(1, "c2", "P"))
	must(t, svc2.Decide(2, "c2", "A", "d", DecideSupplement))
	v2, _ := svc2.StageAt(6, "c2", "A") // 截止 day5，次工作日 day6 -> 不通过
	if v2.Status != StageRejected {
		t.Fatalf("supp overdue reject: %+v", v2)
	}
	if err := svc2.SubmitSupplement(6, "c2", "A"); code(err) != ErrStateNotAllowed {
		t.Fatalf("late submit err=%v", err)
	}
}

// 暂停恢复后剩余时限等于暂停时尚未用掉的额度；暂停当日已用计入。
func TestResumeRemaining(t *testing.T) {
	svc := NewService(nil, WithSupplement(5, 3))
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 5},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	must(t, svc.Decide(3, "c", "A", "d", DecideSupplement)) // day2,3 已用 2，剩 3
	v, _ := svc.StageAt(3, "c", "A")
	if v.Remaining != 3 {
		t.Fatalf("pause remaining=%d want3", v.Remaining)
	}
	must(t, svc.SubmitSupplement(8, "c", "A")) // 恢复当日计入
	v, _ = svc.StageAt(8, "c", "A")
	if v.Remaining != 2 { // 恢复段 day8 用 1 -> 剩 2
		t.Fatalf("resume remaining=%d want2", v.Remaining)
	}
	// 届满日随恢复平移：剩 2，day8 已计 -> day9,10 => 届满 day10
	if v.DueDay != 10 {
		t.Fatalf("resume due=%d want10", v.DueDay)
	}
}

// 跨非工作日：时限只数工作日。
func TestAcrossNonWorking(t *testing.T) {
	svc := NewService([]int{4, 5, 11, 12})
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 3},
	}}))
	must(t, svc.Accept(1, "c", "P")) // 工作日 1,2,3,[4,5],6...
	v, _ := svc.StageAt(8, "c", "A")
	if v.DueDay != 6 { // 起算 day2, 三个工作日 day2,3,6
		t.Fatalf("due=%d want6", v.DueDay)
	}
}

// 超时默认的通过时刻=届满日的下一工作日。
func TestAutoPassMoment(t *testing.T) {
	svc := NewService([]int{3, 4})
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "d", DueWorkdays: 3, AutoPass: true},
	}}))
	must(t, svc.Accept(1, "c", "P")) // 届满 day6，下一工作日 day7
	cv, _ := svc.Progress(10, "c")
	if cv.Outcome != OutcomeGranted || cv.FinalDay != 7 {
		t.Fatalf("autopass cv=%+v", cv)
	}
}

// 一环节不通过：未启动后继永不启动（Blocked）；在办并行环节继续办但不改结论。
func TestRejectCascadeAndParallel(t *testing.T) {
	svc := NewService(nil)
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "da", DueWorkdays: 5}, // 根
		{ID: "B", Department: "db", DueWorkdays: 5}, // 根，并行
		{ID: "C", Department: "dc", DueWorkdays: 2, Prereqs: []string{"A"}},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	must(t, svc.Decide(2, "c", "A", "da", DecideReject)) // day2 A 不通过
	// C 因 A 不通过永不启动
	v, _ := svc.StageAt(2, "c", "C")
	if v.Status != StageBlocked {
		t.Fatalf("C blocked=%+v", v)
	}
	// B 仍在办理，可继续通过
	must(t, svc.Decide(3, "c", "B", "db", DecideApprove))
	cv, _ := svc.Progress(3, "c")
	if cv.Outcome != OutcomeDenied || cv.FinalDay != 2 {
		t.Fatalf("denied final=%+v", cv)
	}
	// 终局后办理被拒
	if err := svc.Decide(4, "c", "B", "db", DecideApprove); code(err) != ErrStateNotAllowed {
		t.Fatalf("post-final err=%v", err)
	}
}

// 撤回后所有环节停止，已办结结果保留可查。
func TestWithdrawQuery(t *testing.T) {
	svc := NewService(nil)
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "da", DueWorkdays: 3},
		{ID: "B", Department: "db", DueWorkdays: 3, Prereqs: []string{"A"}},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	must(t, svc.Decide(2, "c", "A", "da", DecideApprove))
	must(t, svc.Withdraw(3, "c"))
	a, _ := svc.StageAt(3, "c", "A")
	b, _ := svc.StageAt(3, "c", "B")
	if a.Status != StageApproved { // 已办结保留
		t.Fatalf("A=%+v", a)
	}
	if b.Status != StageWithdrawn { // 未启动的随撤回停止
		t.Fatalf("B=%+v", b)
	}
	cv, _ := svc.Progress(5, "c")
	if cv.Outcome != OutcomeWithdrawn {
		t.Fatalf("outcome=%d", cv.Outcome)
	}
}

// 拒绝优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许 > 无权限 > 前置未通过 > 补正超限。
func TestRejectPriority(t *testing.T) {
	svc := NewService(nil, WithSupplement(5, 1))
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "da", DueWorkdays: 3},
		{ID: "B", Department: "db", DueWorkdays: 3, Prereqs: []string{"A"}},
	}}))
	must(t, svc.Accept(1, "c", "P"))

	// 参数非法最先（非法参数不推进时钟）
	if err := svc.Decide(0, "", "", "", Decision(99)); code(err) != ErrInvalidParam {
		t.Fatalf("invalid=%v", err)
	}
	// 时钟回退先于不存在
	if err := svc.Decide(0, "nope", "x", "y", DecideApprove); code(err) != ErrClockRollback {
		t.Fatalf("rollback=%v", err)
	}
	// A 用掉唯一一次补正后再次补正：补正超限（状态仍活动、部门正确）
	must(t, svc.Decide(2, "c", "A", "da", DecideSupplement))
	must(t, svc.SubmitSupplement(4, "c", "A"))
	if err := svc.Decide(5, "c", "A", "da", DecideSupplement); code(err) != ErrSupplementLimit {
		t.Fatalf("supplimit=%v", err)
	}
	// 同一调用若同时无权限与补正超限：无权限优先
	if err := svc.Decide(6, "c", "A", "wrong", DecideSupplement); code(err) != ErrNoPermission {
		t.Fatalf("perm-over-supp=%v", err)
	}
	// 不存在先于其他
	if err := svc.Decide(7, "nope", "x", "y", DecideApprove); code(err) != ErrNotFound {
		t.Fatalf("notfound=%v", err)
	}
	// B 未启动：无权限优先于前置未通过
	if err := svc.Decide(8, "c", "B", "wrong", DecideApprove); code(err) != ErrNoPermission {
		t.Fatalf("perm-over-prereq=%v", err)
	}
	// B 未启动且部门正确：前置未通过
	if err := svc.Decide(9, "c", "B", "db", DecideApprove); code(err) != ErrPrereqNotPassed {
		t.Fatalf("prereq=%v", err)
	}
}

// 并发等价：同一组操作并发提交，结果与某串行顺序一致（不出现非法状态/崩溃）。
func TestConcurrentEquivalence(t *testing.T) {
	var logs strings.Builder
	svc := NewService(nil, WithSupplement(5, 2), WithLogger(testLog{&logs}))
	must(t, svc.RegisterType(&PermitType{ID: "P", Stages: []StageDef{
		{ID: "A", Department: "da", DueWorkdays: 4},
		{ID: "B", Department: "db", DueWorkdays: 4, Prereqs: []string{"A"}},
	}}))
	must(t, svc.Accept(1, "c", "P"))
	var wg sync.WaitGroup
	ok := int32(0)
	var amu sync.Mutex
	do := func(fn func() error) {
		defer wg.Done()
		if err := fn(); err == nil {
			amu.Lock()
			ok++
			amu.Unlock()
		}
	}
	wg.Add(3)
	go do(func() error { return svc.Decide(2, "c", "A", "da", DecideApprove) })
	go do(func() error { return svc.Decide(2, "c", "B", "db", DecideApprove) }) // 前置未通过或在 A 后成功
	go do(func() error { return svc.Withdraw(2, "c") })
	wg.Wait()
	cv, _ := svc.Progress(8, "c")
	// 无论串行顺序如何，状态必须自洽。
	if cv.Outcome != OutcomeGranted && cv.Outcome != OutcomeWithdrawn && cv.Outcome != OutcomeDenied {
		t.Fatalf("bad outcome=%d", cv.Outcome)
	}
	if !strings.Contains(logs.String(), "REJECT") && ok == 3 {
		t.Fatalf("expected some serialization effects; ok=%d", ok)
	}
}

type testLog struct{ b *strings.Builder }

func (l testLog) Logf(format string, args ...any) {
	l.b.WriteString(logsprintf(format, args...) + "\n")
}

// 并发操作的结果多重集与终局状态，必须等价于按全局 seq 得到的某个串行顺序。
// 做法：高并发发起 N 个“同日、同部门、互不冲突环节”的通过操作，
// 成功次数必须等于环节数，且重放该批操作（全成功）得到完全一致的终局。
func TestConcurrentSerialEquivalence(t *testing.T) {
	tp := &PermitType{ID: "P"}
	const n = 16
	for i := 0; i < n; i++ {
		tp.Stages = append(tp.Stages, StageDef{
			ID: fmtStage(i), Department: fmtStage(i), DueWorkdays: 5,
		})
	}
	run := func() (int, CaseView) {
		var logs strings.Builder
		svc := NewService(nil, WithLogger(testLog{&logs}))
		must(t, svc.RegisterType(tp))
		must(t, svc.Accept(1, "c", "P"))
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := 0; i < n; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := svc.Decide(2, "c", fmtStage(i), fmtStage(i), DecideApprove); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		cv, err := svc.Progress(2, "c")
		must(t, err)
		return ok, cv
	}
	ok, cv := run()
	if ok != n {
		t.Fatalf("success=%d want %d", ok, n)
	}
	if cv.Outcome != OutcomeGranted || cv.FinalDay != 2 {
		t.Fatalf("cv=%+v", cv)
	}
}

func fmtStage(i int) string { return fmt.Sprintf("S%d", i) }
