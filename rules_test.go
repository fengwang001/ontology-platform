package ontology

import (
	"errors"
	"testing"
)

func setupEngine(t *testing.T) (*Engine, Config) {
	t.Helper()
	cfg := testConfig()
	e := NewEngine(cfg)
	must(t, e.RegisterTeacher("T1", "math", "teacher"), "register teacher")
	must(t, e.RegisterTeacher("T1", "english", "teacher"), "register teacher2")
	must(t, e.SetUserLevel("teacher", 1), "level teacher")
	must(t, e.SetUserLevel("boss", 3), "level boss")
	must(t, e.SetUserLevel("low", 1), "level low")
	must(t, e.SetUserLevel("sup1", 5), "level sup1")
	must(t, e.SetUserLevel("sup2", 6), "level sup2")
	must(t, e.SetUserLevel("supLow", 4), "level supLow")
	return e, cfg
}

func must(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return "non_coded"
}

func enterInitial(t *testing.T, e *Engine, student, course string, score int, at int64) {
	t.Helper()
	must(t, e.EnterScore("teacher", student, course, "T1", score, at), "enter score")
}

// 窗口终点取等。
func TestReviewWindowBoundary(t *testing.T) {
	e, cfg := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	// 终点取等：恰在窗口终点仍受理。
	must(t, e.ApplyReview("s1", "math", "T1", 100+cfg.ReviewWindow), "apply at window end inclusive")
	must(t, e.RejectReview("boss", "s1", "math", "T1", 100+cfg.ReviewWindow), "reject same instant")
	// 窗口刚过即拒绝。
	if got := codeOf(e.ApplyReview("s1", "math", "T1", 100+cfg.ReviewWindow+1)); got != ErrExpired {
		t.Fatalf("just past window: got %s want %s", got, ErrExpired)
	}
	// 与录入同一时刻（窗口起点取等）也允许（新记录，时钟独立用更大时刻避免干扰）。
	enterInitial(t, e, "s2", "math", 80, 200)
	must(t, e.ApplyReview("s2", "math", "T1", 200), "apply at same instant as entry")
}

// 重复申请被拒；驳回结案后窗口内可再次申请。
func TestDuplicateAndReapply(t *testing.T) {
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply 1")
	if got := codeOf(e.ApplyReview("s1", "math", "T1", 102)); got != ErrState {
		t.Fatalf("duplicate: got %s want %s", got, ErrState)
	}
	must(t, e.RejectReview("teacher", "s1", "math", "T1", 103), "reject")
	must(t, e.ApplyReview("s1", "math", "T1", 104), "reapply after rejection")
}

// 改分上限取等：恰好允许，超过拒绝；分数范围同样取等。
func TestDeltaBoundary(t *testing.T) {
	e, cfg := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 80+cfg.MaxDelta, 102), "delta == cap")
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 103), "approve")
	v, err := e.RecordVersions("s1", "math", "T1")
	must(t, err, "versions")
	if len(v) != 2 || v[1].Score != 85 || v[1].Source != SourceReview || v[1].Effective != 103 {
		t.Fatalf("unexpected versions %+v", v)
	}

	// 再来一轮：超出上限拒绝。
	must(t, e.ApplyReview("s1", "math", "T1", 104), "apply2")
	if got := codeOf(e.CreateProposal("teacher", "s1", "math", "T1", 91, 105)); got != ErrScore {
		t.Fatalf("delta over cap: got %s", got)
	}
	// 分数上下界取等。
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 80, 105), "propose 80")
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 106), "approve2")

	enterInitial(t, e, "s2", "math", 100, 200)
	must(t, e.ApplyReview("s2", "math", "T1", 201), "apply s2")
	if got := codeOf(e.CreateProposal("teacher", "s2", "math", "T1", 101, 202)); got != ErrScore {
		t.Fatalf("above max score: got %s", got)
	}
}

// 审批时限终点取等；超过则惰性失效，且不同触发操作都会落地。
func TestApprovalDeadlineBoundary(t *testing.T) {
	e, cfg := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 82, 102), "proposal")
	// deadline = 102+7 = 109，恰在终点审批有效。
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 102+cfg.ApproveTimeout), "approve at deadline")
	view, err := e.EffectiveAt("s1", "math", "T1", 109)
	must(t, err, "view")
	if view.Score != 82 {
		t.Fatalf("score=%d want 82", view.Score)
	}
}

func triggerLazyExpiry(t *testing.T, trigger string) {
	t.Helper()
	e, cfg := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 82, 102), "proposal")
	deadlinePlus := int64(102 + cfg.ApproveTimeout + 1)

	switch trigger {
	case "propose":
		// 在另一记录上的操作不会影响本记录；这里用本记录的新提案触发：
		// 先落地旧提案失效 -> 申请回到受理中 -> 新提案合法。
		must(t, e.CreateProposal("teacher", "s1", "math", "T1", 83, deadlinePlus), "repropose triggers expiry")
	case "apply":
		// 旧提案失效落地后申请回到受理中；记录已有受理申请，重复申请报状态冲突。
		if got := codeOf(e.ApplyReview("s1", "math", "T1", deadlinePlus)); got != ErrState {
			t.Fatalf("trigger apply: got %s want %s", got, ErrState)
		}
	case "deny":
		// 对已超时提案发起驳回：先落地失效，再无待审批提案 -> 时限错误。
		if got := codeOf(e.DenyProposal("boss", "s1", "math", "T1", deadlinePlus)); got != ErrExpired {
			t.Fatalf("trigger deny: got %s want %s", got, ErrExpired)
		}
	case "approve":
		if got := codeOf(e.ApproveProposal("boss", "s1", "math", "T1", deadlinePlus)); got != ErrExpired {
			t.Fatalf("trigger approve: got %s want %s", got, ErrExpired)
		}
	case "reject":
		must(t, e.RejectReview("boss", "s1", "math", "T1", deadlinePlus), "reject after lazy expiry")
	}

	// 审计里必须存在一条惰性失效记录，且成绩不变。
	var found bool
	for _, a := range e.Audit() {
		if a.Kind == AuditExpire {
			found = true
			if a.At != deadlinePlus {
				t.Fatalf("expire landed at %d want %d", a.At, deadlinePlus)
			}
		}
	}
	if !found {
		t.Fatalf("trigger %s did not land lazy expiry in audit", trigger)
	}
	v, err := e.RecordVersions("s1", "math", "T1")
	must(t, err, "versions")
	if len(v) != 1 || v[0].Score != 80 {
		t.Fatalf("grade changed after lazy expiry: %+v", v)
	}
}

func TestLazyExpiryTriggers(t *testing.T) {
	for _, tr := range []string{"propose", "apply", "deny", "approve", "reject"} {
		t.Run(tr, func(t *testing.T) { triggerLazyExpiry(t, tr) })
	}
}

// 时钟回退：被拒操作不得推进时钟。
func TestClockRewindDoesNotAdvance(t *testing.T) {
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	if got := codeOf(e.ApplyReview("s1", "math", "T1", 99)); got != ErrClock {
		t.Fatalf("clock rewind: got %s want %s", got, ErrClock)
	}
	if e.Clock() != 100 {
		t.Fatalf("clock advanced on rejection: %d", e.Clock())
	}
	// 被拒后同刻/后续正常操作仍可用。
	must(t, e.ApplyReview("s1", "math", "T1", 100), "apply at 100")
}

// 非授课教师提案、级别不足、自审。
func TestPermissions(t *testing.T) {
	e, _ := setupEngine(t)
	must(t, e.SetUserLevel("otherTeacher", 1), "level other")
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")

	if got := codeOf(e.CreateProposal("otherTeacher", "s1", "math", "T1", 81, 102)); got != ErrForbidden {
		t.Fatalf("non-teacher propose: got %s", got)
	}
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 81, 102), "propose")
	if got := codeOf(e.ApproveProposal("teacher", "s1", "math", "T1", 103)); got != ErrForbidden {
		t.Fatalf("self approve: got %s", got)
	}
	if got := codeOf(e.ApproveProposal("low", "s1", "math", "T1", 103)); got != ErrForbidden {
		t.Fatalf("low level: got %s", got)
	}
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 103), "boss approves")
}

// 驳回提案后可再次提案，最终通过。
func TestDenyThenReproposeFlow(t *testing.T) {
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 84, 102), "proposal1")
	must(t, e.DenyProposal("boss", "s1", "math", "T1", 103), "deny1")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 82, 104), "proposal2")
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 105), "approve2")
	v, err := e.RecordVersions("s1", "math", "T1")
	must(t, err, "versions")
	if len(v) != 2 || v[1].Score != 82 || v[1].Effective != 105 {
		t.Fatalf("unexpected %+v", v)
	}
	// 审计次序：申请、提案、驳回、提案、通过。
	kinds := []AuditKind{}
	for _, a := range e.Audit() {
		kinds = append(kinds, a.Kind)
	}
	want := []AuditKind{AuditReview, AuditProposal, AuditDeny, AuditProposal, AuditApprove}
	if len(kinds) != len(want) {
		t.Fatalf("audit kinds=%v want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("audit[%d]=%s want %s", i, kinds[i], want[i])
		}
	}
}

// 锁定时清理进行中申请/提案；锁定后普通流程全拒绝；锁不可撤销。
func TestLockCleanup(t *testing.T) {
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	enterInitial(t, e, "s2", "math", 70, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply s1")
	must(t, e.ApplyReview("s2", "math", "T1", 101), "apply s2")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 82, 102), "proposal s1")

	must(t, e.LockTerm("boss", "T1", 105), "lock")

	var voidN, closeN int
	for _, a := range e.Audit() {
		switch a.Kind {
		case AuditLockVoid:
			voidN++
		case AuditLockClose:
			closeN++
		}
	}
	if voidN != 1 || closeN != 2 {
		t.Fatalf("lock cleanup void=%d close=%d, want 1,2", voidN, closeN)
	}
	if got := codeOf(e.ApplyReview("s1", "math", "T1", 106)); got != ErrLocked {
		t.Fatalf("apply after lock: got %s", got)
	}
	if got := codeOf(e.CreateProposal("teacher", "s1", "math", "T1", 83, 106)); got != ErrLocked {
		t.Fatalf("propose after lock: got %s", got)
	}
	if got := codeOf(e.LockTerm("boss", "T1", 107)); got != ErrLocked {
		t.Fatalf("double lock: got %s", got)
	}
	if !e.IsLocked("T1") {
		t.Fatal("term should be locked")
	}
	// 成绩未变。
	v, _ := e.RecordVersions("s1", "math", "T1")
	if len(v) != 1 || v[0].Score != 80 {
		t.Fatalf("grade changed on lock: %+v", v)
	}
}

// 特殊通道：级别、两人互异、第一人有效期取等与过期、超单次上限允许。
func TestSpecialChannel(t *testing.T) {
	e, cfg := setupEngine(t)
	must(t, e.RegisterTeacher("T2", "math", "teacher"), "register T2")
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.EnterScore("teacher", "s1", "math", "T2", 50, 100), "enter T2 score")
	must(t, e.LockTerm("boss", "T1", 101), "lock")

	// 未锁定学期不能走特殊通道。
	if got := codeOf(e.SpecialFirst("sup1", "s1", "math", "T2", 95, 201)); got != ErrState {
		t.Fatalf("special before lock: got %s", got)
	}

	// 级别不足。
	if got := codeOf(e.SpecialFirst("supLow", "s1", "math", "T1", 95, 202)); got != ErrForbidden {
		t.Fatalf("special low level: got %s", got)
	}
	// 大幅改分（超过 MaxDelta=5）在特殊通道允许。
	must(t, e.SpecialFirst("sup1", "s1", "math", "T1", 95, 202), "first")
	// 第一人不能自己二次确认。
	if got := codeOf(e.SpecialSecond("sup1", "s1", "math", "T1", 203)); got != ErrForbidden {
		t.Fatalf("same approver: got %s", got)
	}
	// 有效期终点取等：202+3=205。
	must(t, e.SpecialSecond("sup2", "s1", "math", "T1", 202+cfg.FirstValidFor), "second at expiry end")
	v, _ := e.RecordVersions("s1", "math", "T1")
	if v[len(v)-1].Score != 95 || v[len(v)-1].Source != SourceSpecial || v[len(v)-1].Effective != 205 {
		t.Fatalf("special version wrong: %+v", v)
	}

	// 第一人过期：第二人确认失败，第一人确认被清除，可重新发起。
	must(t, e.SpecialFirst("sup1", "s1", "math", "T1", 90, 210), "first again")
	if got := codeOf(e.SpecialSecond("sup2", "s1", "math", "T1", 210+cfg.FirstValidFor+1)); got != ErrExpired {
		t.Fatalf("first expired: got %s", got)
	}
	must(t, e.SpecialFirst("sup2", "s1", "math", "T1", 91, 215), "first replaced by other")
	must(t, e.SpecialSecond("sup1", "s1", "math", "T1", 216), "second")
	v, _ = e.RecordVersions("s1", "math", "T1")
	if v[len(v)-1].Score != 91 {
		t.Fatalf("final special score: %+v", v)
	}
}

// 时点查询跨多个版本，且不受后续版本影响；复核中标志正确。
func TestPointInTimeAcrossVersions(t *testing.T) {
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	enterInitial(t, e, "s1", "english", 60, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 82, 102), "proposal")
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 103), "approve v2")
	must(t, e.ApplyReview("s1", "math", "T1", 104), "apply2")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 84, 105), "proposal2")
	must(t, e.ApproveProposal("boss", "s1", "math", "T1", 106), "approve v3")

	cases := []struct {
		at       int64
		score    int
		source   Source
		inReview bool
	}{
		{99, 0, "", false}, // 尚无成绩
		{100, 80, SourceInitial, false},
		{101, 80, SourceInitial, true},
		{102, 80, SourceInitial, true},
		{103, 82, SourceReview, false},
		{105, 82, SourceReview, true},
		{106, 84, SourceReview, false},
		{1_000_000, 84, SourceReview, false},
	}
	for _, c := range cases {
		view, err := e.EffectiveAt("s1", "math", "T1", c.at)
		if c.score == 0 && c.source == "" {
			if codeOf(err) != ErrNotFound {
				t.Fatalf("at %d: expected not-found, got %v / %+v", c.at, err, view)
			}
			continue
		}
		if err != nil {
			t.Fatalf("at %d: %v", c.at, err)
		}
		if view.Score != c.score || view.Source != c.source || view.InReview != c.inReview {
			t.Fatalf("at %d: got %+v want score=%d src=%s review=%v", c.at, view, c.score, c.source, c.inReview)
		}
	}

	avg, n, err := e.TermAverageAt("s1", "T1", 103)
	must(t, err, "avg")
	if n != 2 || avg != float64(82+60)/2 {
		t.Fatalf("avg=%v n=%d", avg, n)
	}
	avg100, n100, err := e.TermAverageAt("s1", "T1", 100)
	must(t, err, "avg100")
	if n100 != 2 || avg100 != 70 {
		t.Fatalf("avg100=%v n=%d", avg100, n100)
	}
}

// 拒绝优先级逐对验证：对每对相邻分类构造同时满足两者的调用，断言返回更高优先级码。
func TestErrorPriorityPairs(t *testing.T) {
	pairs := []struct {
		higher, lower string
		setup         func(t *testing.T, e *Engine)
		invoke        func(e *Engine) error
	}{
		{ErrInvalid, ErrClock, nil,
			func(e *Engine) error { return e.ApplyReview("", "c", "T1", -1) }},
		{ErrClock, ErrNotFound,
			func(t *testing.T, e *Engine) { enterInitial(t, e, "s9", "math", 80, 100) },
			func(e *Engine) error { return e.ApplyReview("nobody", "math", "T1", 1) }},
		{ErrNotFound, ErrLocked,
			func(t *testing.T, e *Engine) { must(t, e.LockTerm("boss", "T1", 500), "lock early") },
			func(e *Engine) error { return e.ApplyReview("ghost", "math", "T1", 501) }},
		{ErrLocked, ErrForbidden,
			func(t *testing.T, e *Engine) {
				enterInitial(t, e, "s1", "math", 80, 400)
				must(t, e.LockTerm("boss", "T1", 500), "lock")
			},
			func(e *Engine) error { return e.CreateProposal("stranger", "s1", "math", "T1", 81, 600) }},
		{ErrForbidden, ErrExpired,
			// 提案已超时未落地；自审者审批：自审(5)优先于时限(6)。
			func(t *testing.T, e *Engine) {
				enterInitial(t, e, "s1", "math", 80, 100)
				must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
				must(t, e.CreateProposal("teacher", "s1", "math", "T1", 81, 102), "proposal")
			},
			func(e *Engine) error { return e.ApproveProposal("teacher", "s1", "math", "T1", 900) }},
		{ErrExpired, ErrState,
			// 窗口已过且存在未结案申请：时限(6)优先于状态(7)。
			func(t *testing.T, e *Engine) {
				enterInitial(t, e, "s1", "math", 80, 100)
				must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
			},
			func(e *Engine) error { return e.ApplyReview("s1", "math", "T1", 901) }},
		{ErrState, ErrScore,
			// 已有待审批提案；再次提案且分数越界。
			func(t *testing.T, e *Engine) {
				enterInitial(t, e, "s1", "math", 80, 100)
				must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
				must(t, e.CreateProposal("teacher", "s1", "math", "T1", 81, 102), "proposal")
			},
			// 同一时刻第二次提案：旧提案仍待审批 -> 状态冲突优先于分数越界。
			func(e *Engine) error { return e.CreateProposal("teacher", "s1", "math", "T1", 999, 102) }},
	}

	for i, p := range pairs {
		e, _ := setupEngine(t)
		if p.setup != nil {
			p.setup(t, e)
		}
		err := p.invoke(e)
		if got := codeOf(err); got != p.higher {
			t.Fatalf("pair %d (%s>%s): got %s (%v)", i, p.higher, p.lower, got, err)
		}
	}

	// 不变量抽查：第 7 对的引擎中，拒绝未改动状态、不记审计。
	e, _ := setupEngine(t)
	enterInitial(t, e, "s1", "math", 80, 100)
	must(t, e.ApplyReview("s1", "math", "T1", 101), "apply")
	must(t, e.CreateProposal("teacher", "s1", "math", "T1", 81, 102), "proposal")
	if got := codeOf(e.CreateProposal("teacher", "s1", "math", "T1", 999, 102)); got != ErrState {
		t.Fatalf("final state/score pair: got %s", got)
	}
	v, _ := e.RecordVersions("s1", "math", "T1")
	if len(v) != 1 || v[0].Score != 80 {
		t.Fatalf("state mutated by rejected op: %+v", v)
	}
}
