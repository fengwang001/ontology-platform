package xueji

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func testCal(t *testing.T, n int) *Calendar {
	t.Helper()
	sems := make([]Semester, n)
	for i := range sems {
		sems[i] = Semester{Start: int64(i * 1000), End: int64(i*1000 + 1000), Deadline: int64(i*1000 + 500)}
	}
	cal, err := NewCalendar(sems)
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func testCfg() Config {
	return Config{
		ApprovalLevels: map[AppType]int{
			AppSuspend: 1, AppResume: 2, AppTransfer: 2, AppRetain: 1, AppWithdraw: 1,
		},
		TimeLimit:           300,
		MaxSuspendSemesters: 2,
		MaxRetainSemesters:  1,
		MaxStudySemesters:   0,
	}
}

func newEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := NewEngine(testCal(t, 12), cfg)
	if err != nil {
		t.Fatal(err)
	}
	must(t, e.AddMajor("cs", 1, 0))
	must(t, e.AddMajor("math", 0, 0))
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func addStudent(t *testing.T, e *Engine, id, major string, sem int, now int64) {
	t.Helper()
	must(t, e.AddStudent(id, major, sem, now))
}

func submit(t *testing.T, e *Engine, sid string, typ AppType, target, submitter string, now int64) string {
	t.Helper()
	id, err := e.Submit(sid, typ, target, submitter, now)
	must(t, err)
	return id
}

func wantErr(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("期望错误 [%s], 实际 %v", code, err)
	}
}

func wantSnap(t *testing.T, e *Engine, sid string, at int64, state State, major string, pending bool) {
	t.Helper()
	s, err := e.QueryAt(sid, at)
	must(t, err)
	if !s.Found || s.State != state || s.Major != major || s.Pending != pending {
		t.Fatalf("QueryAt(%s,%d) = %+v, 期望 %s/%s/pending=%v", sid, at, s, state, major, pending)
	}
}

// 截止时刻取等：恰等于截止时刻提交视为"之前"，自当前学期起生效；
// 晚一个时刻则自下一学期起生效，期间状态不变；转专业逾期提交报可区分错误。
func TestDeadlineEquality(t *testing.T) {
	e := newEngine(t, testCfg())
	addStudent(t, e, "s1", "cs", 0, 0)
	addStudent(t, e, "s2", "cs", 0, 0)

	// now == Deadline(1500)：生效当前学期（学期 1，起始 1000）
	app1 := submit(t, e, "s1", AppSuspend, "", "adm", 1500)
	must(t, e.Approve(app1, "a1", 1501))
	wantSnap(t, e, "s1", 999, StateEnrolled, "cs", false)
	wantSnap(t, e, "s1", 1000, StateSuspended, "cs", false)

	// now == 1501 > Deadline：生效下一学期（学期 2，起始 2000）
	app2 := submit(t, e, "s2", AppSuspend, "", "adm", 1501)
	must(t, e.Approve(app2, "a1", 1502))
	wantSnap(t, e, "s2", 1999, StateEnrolled, "cs", false)
	wantSnap(t, e, "s2", 2000, StateSuspended, "cs", false)

	// 转专业在截止之后提交：报"时限已过或截止已过"
	_, err := e.Submit("s2", AppTransfer, "cs", "adm", 1503)
	wantErr(t, err, ErrDeadlinePassed)
}

// 时点查询落在学期边界：学期左闭右开，边界时刻归属新学期；
// 未结案标志按提交/结案时刻精确判定。
func TestQueryAtSemesterBoundary(t *testing.T) {
	e := newEngine(t, testCfg())
	addStudent(t, e, "s1", "cs", 0, 0)
	app := submit(t, e, "s1", AppSuspend, "", "adm", 900) // 截止后提交，生效学期 1
	wantSnap(t, e, "s1", 899, StateEnrolled, "cs", false)
	wantSnap(t, e, "s1", 900, StateEnrolled, "cs", true) // 提交时刻起未结案
	must(t, e.Approve(app, "a1", 950))
	wantSnap(t, e, "s1", 949, StateEnrolled, "cs", true)
	wantSnap(t, e, "s1", 950, StateEnrolled, "cs", false) // 结案时刻起不再未结案
	wantSnap(t, e, "s1", 999, StateEnrolled, "cs", false)
	wantSnap(t, e, "s1", 1000, StateSuspended, "cs", false) // 学期边界归属新学期
}

// 休学累计恰等于上限的申请允许，超出的拒绝；累计数等于已生效休学学期数。
func TestSuspendCapExact(t *testing.T) {
	e := newEngine(t, testCfg()) // MaxSuspendSemesters = 2
	addStudent(t, e, "s1", "cs", 0, 0)

	must(t, e.Approve(submit(t, e, "s1", AppSuspend, "", "adm", 1000), "a1", 1001)) // 休学@学期1
	r1 := submit(t, e, "s1", AppResume, "", "adm", 2000)
	must(t, e.Approve(r1, "a1", 2001))
	must(t, e.Approve(r1, "a2", 2002)) // 复学@学期2，第一段休学计 1 学期

	// 累计 1，再申请使累计恰为 2（=上限）：允许
	must(t, e.Approve(submit(t, e, "s1", AppSuspend, "", "adm", 3000), "a1", 3001)) // 休学@学期3
	r2 := submit(t, e, "s1", AppResume, "", "adm", 4000)
	must(t, e.Approve(r2, "a1", 4001))
	must(t, e.Approve(r2, "a2", 4002)) // 复学@学期4，第二段休学计 1 学期

	susp, _, _, err := e.LedgerAt("s1", 4003)
	must(t, err)
	if susp != 2 {
		t.Fatalf("累计休学 = %d, 期望 2", susp)
	}
	// 累计 2，再申请使累计达 3（>上限）：拒绝，且不改动任何状态、不入审计
	auditLen := len(e.Audit())
	_, err = e.Submit("s1", AppSuspend, "", "adm", 4004)
	wantErr(t, err, ErrCapExceeded)
	if len(e.Audit()) != auditLen {
		t.Fatal("被拒绝的操作不得记入审计")
	}
	must(t, e.Validate(4004))
}

// 年限用尽后的惰性退学：已用年限达到上限后，下一次触及该学生的操作
// 惰性落地为退学（入审计），操作本身报终态错误；落地前的历史时点查询不受影响。
func TestLazyWithdrawalOnYearsExhausted(t *testing.T) {
	cfg := testCfg()
	cfg.MaxStudySemesters = 2
	e := newEngine(t, cfg)
	addStudent(t, e, "s1", "cs", 0, 0)

	// 学期 2 已用年限 = 3 >= 2：提交操作触及该学生，先惰性落地退学
	_, err := e.Submit("s1", AppSuspend, "", "adm", 2000)
	wantErr(t, err, ErrTerminal)
	wantSnap(t, e, "s1", 2000, StateWithdrawn, "cs", false)
	// 惰性：学期 1 期间年限虽已达上限，但未触及，历史时点仍为在读
	wantSnap(t, e, "s1", 1500, StateEnrolled, "cs", false)

	found := false
	for _, ev := range e.Audit() {
		if ev.Kind == "lazy-withdraw" && ev.StudentID == "s1" {
			found = true
		}
	}
	if !found {
		t.Fatal("审计中应包含 lazy-withdraw 事件")
	}
	// 再次触及仍为终态
	_, err = e.Submit("s1", AppResume, "", "adm", 2001)
	wantErr(t, err, ErrTerminal)
}

// 申请时限取等：提交后恰等于时限仍有效，超过一个时刻则自动作废，
// 作废在下一次触及该申请时惰性落地。
func TestTimeLimitEquality(t *testing.T) {
	cfg := testCfg()
	cfg.ApprovalLevels[AppSuspend] = 2
	cfg.TimeLimit = 300
	e := newEngine(t, cfg)
	addStudent(t, e, "s1", "cs", 0, 0)
	addStudent(t, e, "s2", "cs", 0, 0)

	a1 := submit(t, e, "s1", AppSuspend, "", "adm", 1000)
	a2 := submit(t, e, "s2", AppSuspend, "", "adm", 1000)

	// now - submit == 300 == 时限：仍有效，两级均可完成
	must(t, e.Approve(a1, "u1", 1300))
	must(t, e.Approve(a1, "u2", 1300))
	wantSnap(t, e, "s1", 1000, StateSuspended, "cs", true) // 申请 1000 提交、1300 结案

	// now - submit == 301 > 时限：触及即作废，报"时限已过"
	wantErr(t, e.Approve(a2, "u1", 1301), ErrDeadlinePassed)
	wantErr(t, e.Approve(a2, "u2", 1302), ErrDeadlinePassed)
	// 时点查询：t=1300 恰等于时限仍视为未结案，t=1301 起视为已作废
	wantSnap(t, e, "s2", 1300, StateEnrolled, "cs", true)
	wantSnap(t, e, "s2", 1301, StateEnrolled, "cs", false)
	// 作废后该学生可重新提交
	submit(t, e, "s2", AppSuspend, "", "adm", 1303)
}

// 多层审批中途驳回：任一层驳回即结案，此后该申请不可再审，学生可重新申请。
func TestMultiLevelMidReject(t *testing.T) {
	cfg := testCfg()
	cfg.ApprovalLevels[AppTransfer] = 3
	e := newEngine(t, cfg)
	addStudent(t, e, "s1", "cs", 0, 0)

	app := submit(t, e, "s1", AppTransfer, "cs", "adm", 100)
	must(t, e.Approve(app, "u1", 101)) // 第 1 层通过
	must(t, e.Reject(app, "u2", 102))  // 第 2 层驳回，结案
	wantErr(t, e.Approve(app, "u3", 103), ErrStateNotAllowed)
	wantErr(t, e.Reject(app, "u3", 103), ErrStateNotAllowed)
	wantSnap(t, e, "s1", 103, StateEnrolled, "cs", false)
	// 学生可重新提交新申请
	submit(t, e, "s1", AppSuspend, "", "adm", 104)
}

// 审批人不得审批本人提交的申请；同一审批人不得在同一申请的两个层级重复出现。
func TestApproverConstraints(t *testing.T) {
	cfg := testCfg()
	cfg.ApprovalLevels[AppSuspend] = 2
	e := newEngine(t, cfg)
	addStudent(t, e, "s1", "cs", 0, 0)

	app := submit(t, e, "s1", AppSuspend, "", "alice", 600)   // 截止后提交，生效学期 1
	wantErr(t, e.Approve(app, "alice", 601), ErrNoPermission) // 自审
	must(t, e.Approve(app, "bob", 602))                       // 第 1 层
	wantErr(t, e.Approve(app, "bob", 603), ErrNoPermission)   // 重复出现于第 2 层
	wantErr(t, e.Reject(app, "bob", 603), ErrNoPermission)    // 驳回同样受限
	must(t, e.Approve(app, "carol", 604))                     // 第 2 层通过
	wantSnap(t, e, "s1", 1000, StateSuspended, "cs", false)
}

// 名额不足时最终层级报可区分错误且申请保持待审；
// 管理操作追加名额后再次审批通过，名额随之扣减。
func TestTransferQuotaRetry(t *testing.T) {
	e := newEngine(t, testCfg()) // math 每学期名额 0
	addStudent(t, e, "s1", "cs", 0, 0)
	addStudent(t, e, "s2", "cs", 0, 0)

	app := submit(t, e, "s1", AppTransfer, "math", "adm", 1100) // 截止前，生效学期 1
	must(t, e.Approve(app, "a1", 1101))
	wantErr(t, e.Approve(app, "a2", 1102), ErrQuotaInsufficient)
	// 申请保持待审：该学生不能再提交，层级未被消费
	_, err := e.Submit("s1", AppSuspend, "", "adm", 1103)
	wantErr(t, err, ErrPendingExists)
	wantSnap(t, e, "s1", 1103, StateEnrolled, "cs", true)

	// 管理操作追加名额后重审通过
	must(t, e.AddQuota("math", 1, 1, 1104))
	must(t, e.Approve(app, "a2", 1105))
	wantSnap(t, e, "s1", 999, StateEnrolled, "cs", false)
	wantSnap(t, e, "s1", 1000, StateEnrolled, "math", false) // 在读不变，仅专业变更

	// 名额已扣减：另一学生同学期转入再次名额不足
	app2 := submit(t, e, "s2", AppTransfer, "math", "adm", 1106)
	must(t, e.Approve(app2, "a1", 1107))
	wantErr(t, e.Approve(app2, "a2", 1108), ErrQuotaInsufficient)
}

// 审批时刻晚、生效时刻早的变更不得出现：生效时刻不晚于当前最新版本
// 生效时刻的申请在最终层级被拒（可区分错误），版本链保持严格递增。
func TestEffectiveTooEarlyRejected(t *testing.T) {
	e := newEngine(t, testCfg())
	addStudent(t, e, "s1", "cs", 0, 0)

	must(t, e.Approve(submit(t, e, "s1", AppSuspend, "", "adm", 1000), "a1", 1001)) // 休学@1000
	r := submit(t, e, "s1", AppResume, "", "adm", 1600)                             // 截止后，生效学期 2
	must(t, e.Approve(r, "a1", 1601))
	must(t, e.Approve(r, "a2", 1602)) // 复学@2000（未来版本）

	// 学期 1 内再提交退学：生效时刻 1000 <= 最新版本 2000
	w := submit(t, e, "s1", AppWithdraw, "", "adm", 1700)
	wantErr(t, e.Approve(w, "a1", 1701), ErrEffectiveTooEarly)
	// 申请已结案驳回，状态未被改动
	wantSnap(t, e, "s1", 1701, StateSuspended, "cs", false)
	wantSnap(t, e, "s1", 2000, StateEnrolled, "cs", false)
	must(t, e.Validate(1701))
	// 学生可继续申请（生效学期 3 晚于最新版本）
	submit(t, e, "s1", AppSuspend, "", "adm", 2501)
}

// 被拒绝的操作不得推进任何时钟。
func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	e := newEngine(t, testCfg())
	addStudent(t, e, "s1", "cs", 0, 0)
	submit(t, e, "s1", AppSuspend, "", "adm", 600) // 时钟推进到 600
	_, err := e.Submit("s1", AppWithdraw, "", "adm", 700)
	wantErr(t, err, ErrPendingExists) // 被拒绝，时钟停留在 600
	// 若时钟被推进到 700，则 650 会报时钟回退；实际应被接受
	must(t, e.AddQuota("cs", 0, 1, 650))
}

// 错误分类固定优先级的逐对验证：每个用例构造同时违反两类约束的操作，
// 断言返回优先级更高（声明序更小）的错误。
// 注：无权限(8) 与累计上限(9)、累计上限(9) 与生效时刻(10) 分属审批/提交
// 两类操作，无法在单个操作上同时触发，故以 (8,10)、(8,11)、(10,11) 覆盖相邻序。
func TestErrorPriorityPairs(t *testing.T) {
	type pair struct {
		name string
		op   func(t *testing.T, e *Engine) error
		want ErrCode
	}
	cases := []pair{
		{"参数非法>时钟回退", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.AddQuota("cs", 0, 1, 500)) // 时钟 = 500
			_, err := e.Submit("", AppSuspend, "", "x", 100)
			return err
		}, ErrInvalidParam},
		{"时钟回退>不存在", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.AddQuota("cs", 0, 1, 500))
			_, err := e.Submit("ghost", AppSuspend, "", "x", 100)
			return err
		}, ErrClockRegression},
		{"不存在>终态", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.Approve(submit(t, e, "s1", AppWithdraw, "", "adm", 600), "a1", 601))
			_, err := e.Submit("s1", AppTransfer, "ghost", "adm", 1200) // 目标专业不存在 + 学生终态
			return err
		}, ErrNotFound},
		{"终态>状态不允许", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.Approve(submit(t, e, "s1", AppWithdraw, "", "adm", 600), "a1", 601))
			_, err := e.Submit("s1", AppSuspend, "", "adm", 1200) // 终态 + 退学后不可休学
			return err
		}, ErrTerminal},
		{"状态不允许>已有未结案", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.Approve(submit(t, e, "s1", AppSuspend, "", "adm", 600), "a1", 601)) // 休学@1000
			submit(t, e, "s1", AppResume, "", "adm", 1100)                                // 未结案复学
			_, err := e.Submit("s1", AppSuspend, "", "adm", 1200)                         // 休学中不可休学 + 有未结案
			return err
		}, ErrStateNotAllowed},
		{"已有未结案>截止已过", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			submit(t, e, "s1", AppSuspend, "", "adm", 1400)          // 未结案（时限 300 内）
			_, err := e.Submit("s1", AppTransfer, "cs", "adm", 1600) // 未结案 + 转专业逾期
			return err
		}, ErrPendingExists},
		{"时限已过>无权限", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			app := submit(t, e, "s1", AppSuspend, "", "alice", 1200)
			return e.Approve(app, "alice", 1501) // 已超时限(300)作废 + 自审
		}, ErrDeadlinePassed},
		{"无权限>生效时刻过早", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			must(t, e.Approve(submit(t, e, "s1", AppRetain, "", "adm", 600), "a1", 601)) // 保留@1000
			r := submit(t, e, "s1", AppResume, "", "adm", 1600)
			must(t, e.Approve(r, "a1", 1601))
			must(t, e.Approve(r, "a2", 1602)) // 复学@2000
			w := submit(t, e, "s1", AppWithdraw, "", "alice", 1700)
			return e.Approve(w, "alice", 1701) // 自审 + 生效时刻 1000 <= 2000
		}, ErrNoPermission},
		{"无权限>名额不足", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			app := submit(t, e, "s1", AppTransfer, "math", "alice", 1100) // math 名额 0
			must(t, e.Approve(app, "a1", 1101))
			return e.Approve(app, "alice", 1102) // 自审 + 名额不足
		}, ErrNoPermission},
		{"生效时刻过早>名额不足", func(t *testing.T, e *Engine) error {
			addStudent(t, e, "s1", "cs", 0, 0)
			x := submit(t, e, "s1", AppTransfer, "cs", "adm", 1100) // 截止前，生效学期 1
			must(t, e.Approve(x, "a1", 1101))
			must(t, e.Approve(x, "a2", 1102)) // 版本@1000，cs 学期1 名额用尽
			y := submit(t, e, "s1", AppTransfer, "cs", "adm", 1200)
			must(t, e.Approve(y, "a1", 1201))
			return e.Approve(y, "a2", 1202) // 生效时刻 1000 <= 1000 + 名额不足
		}, ErrEffectiveTooEarly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEngine(t, testCfg())
			wantErr(t, c.op(t, e), c.want)
		})
	}
}

// 并发调用等价于某个串行顺序：多 goroutine 混合调用后不变量必须成立。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	e := newEngine(t, testCfg())
	ids := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"}
	for _, id := range ids {
		addStudent(t, e, id, "cs", 0, 0)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				now := r.Int63n(12000)
				sid := ids[r.Intn(len(ids))]
				switch r.Intn(6) {
				case 0:
					_, _ = e.Submit(sid, AppType(r.Intn(5)), "math", "adm", now)
				case 1:
					_ = e.Approve(fmt.Sprintf("app-%d", r.Intn(200)+1), "a1", now)
				case 2:
					_ = e.Reject(fmt.Sprintf("app-%d", r.Intn(200)+1), "a2", now)
				case 3:
					_, _ = e.QueryAt(sid, now)
				case 4:
					_, _, _, _ = e.LedgerAt(sid, now)
				case 5:
					_ = e.AddQuota("math", r.Intn(12), 1, now)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	must(t, e.Validate(11000))
}
