package qc

import (
	"reflect"
	"testing"
)

// newTestSystem 登记一个标准项目：仪器 I1、项目 A1，
// 低水平靶值 100、SD 10，高水平靶值 200、SD 20，有效期 50 秒。
func newTestSystem(t *testing.T) *System {
	t.Helper()
	s := New()
	if err := s.RegisterAnalyte("I1", "A1", 100, 10, 200, 20, 50); err != nil {
		t.Fatalf("RegisterAnalyte: %v", err)
	}
	return s
}

func mustRun(t *testing.T, s *System, low, high, now int64) RunResult {
	t.Helper()
	res, err := s.Run("I1", "A1", low, high, now)
	if err != nil {
		t.Fatalf("Run(%d,%d,%d): %v", low, high, now, err)
	}
	return res
}

func runN(t *testing.T, s *System, low, high int64, from int64, n int) RunResult {
	t.Helper()
	var res RunResult
	for i := 0; i < n; i++ {
		res = mustRun(t, s, low, high, from+int64(i))
	}
	return res
}

func mustIssue(t *testing.T, s *System, now int64) int64 {
	t.Helper()
	id, err := s.IssueReport("I1", "A1", now)
	if err != nil {
		t.Fatalf("IssueReport(%d): %v", now, err)
	}
	return id
}

func issueErr(t *testing.T, s *System, now int64) error {
	t.Helper()
	_, err := s.IssueReport("I1", "A1", now)
	return err
}

func reportStatus(t *testing.T, s *System, id int64) ReportStatus {
	t.Helper()
	st, err := s.ReportStatus(id)
	if err != nil {
		t.Fatalf("ReportStatus(%d): %v", id, err)
	}
	return st
}

func projectState(t *testing.T, s *System) ProjectState {
	t.Helper()
	st, err := s.ProjectState("I1", "A1")
	if err != nil {
		t.Fatalf("ProjectState: %v", err)
	}
	return st
}

func assertRules(t *testing.T, res RunResult, want []RuleID, wantStatus RunStatus) {
	t.Helper()
	if !reflect.DeepEqual(res.Rules, want) || res.Status != wantStatus {
		t.Fatalf("got rules=%v status=%v, want rules=%v status=%v",
			res.Rules, res.Status, want, wantStatus)
	}
}

// 规则一：恰等于 3 倍标准差不触发，差一则触发；两个水平各自检验。
func TestRule1Boundary(t *testing.T) {
	// 低水平 SD=10，3 倍为 30：偏离 +30 不触发（恰等），+31 触发。
	s := newTestSystem(t)
	assertRules(t, mustRun(t, s, 130, 200, 0), nil, RunWarning)
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 131, 200, 0), []RuleID{Rule1}, RunRejected)
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 69, 200, 0), []RuleID{Rule1}, RunRejected)
	// 高水平 SD=20，3 倍为 60：偏离 -60 不触发，-61 触发。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 100, 140, 0), nil, RunWarning)
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 100, 139, 0), []RuleID{Rule1}, RunRejected)
}

// 规则二：同一水平连续两点同侧且都超过 2 倍标准差；恰等不算，异侧不算。
func TestRule2Boundary(t *testing.T) {
	// 恰等于 2 倍标准差（+20）不算超过，其后 +21、+21 才构成连续两点。
	s := newTestSystem(t)
	assertRules(t, mustRun(t, s, 120, 200, 0), nil, RunNormal)
	assertRules(t, mustRun(t, s, 121, 200, 1), nil, RunWarning)
	assertRules(t, mustRun(t, s, 121, 200, 2), []RuleID{Rule2}, RunRejected)

	// 异侧打断：+21 后 -21 不构成规则二。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 121, 200, 0), nil, RunWarning)
	assertRules(t, mustRun(t, s, 79, 200, 1), nil, RunWarning)
	assertRules(t, mustRun(t, s, 79, 200, 2), []RuleID{Rule2}, RunRejected)

	// 高水平独立计数，与低水平互不干扰。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 100, 241, 0), nil, RunWarning)
	assertRules(t, mustRun(t, s, 121, 241, 1), []RuleID{Rule2}, RunRejected)
}

// 规则三：本次运行两水平异侧且各超 2 倍自身标准差。
func TestRule3(t *testing.T) {
	s := newTestSystem(t)
	assertRules(t, mustRun(t, s, 121, 159, 0), []RuleID{Rule3}, RunRejected)
	// 反向同样触发。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 79, 241, 0), []RuleID{Rule3}, RunRejected)
	// 高水平恰等 2 倍标准差（-40）不触发，仅警告。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 121, 160, 0), nil, RunWarning)
	// 同侧都超 2 倍标准差不触发规则三。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 121, 241, 0), nil, RunWarning)
}

// 规则四：同一水平连续四点同侧且都超过 1 倍标准差；恰等与零均中断。
func TestRule4Boundary(t *testing.T) {
	// 偏离 +11 超过 1 倍标准差（10），第四次触发。
	s := newTestSystem(t)
	assertRules(t, runN(t, s, 111, 200, 0, 3), nil, RunNormal)
	assertRules(t, mustRun(t, s, 111, 200, 3), []RuleID{Rule4}, RunRejected)

	// 恰等于 1 倍标准差（+10）不算超过，四点不触发。
	s = newTestSystem(t)
	assertRules(t, runN(t, s, 110, 200, 0, 4), nil, RunNormal)

	// 中间一个恰等点打断后重新计数。
	s = newTestSystem(t)
	runN(t, s, 111, 200, 0, 2)
	assertRules(t, mustRun(t, s, 110, 200, 2), nil, RunNormal)
	assertRules(t, runN(t, s, 111, 200, 3, 3), nil, RunNormal)
	assertRules(t, mustRun(t, s, 111, 200, 6), []RuleID{Rule4}, RunRejected)

	// 偏离量为零打断连续。
	s = newTestSystem(t)
	runN(t, s, 111, 200, 0, 3)
	assertRules(t, mustRun(t, s, 100, 200, 3), nil, RunNormal)
	assertRules(t, runN(t, s, 111, 200, 4, 3), nil, RunNormal)
	assertRules(t, mustRun(t, s, 111, 200, 7), []RuleID{Rule4}, RunRejected)
}

// 规则五：同一水平连续十点同侧；偏离量为零或异侧中断。
func TestRule5Boundary(t *testing.T) {
	// 偏离 +1（不超过 1 倍标准差）连续十点同侧触发。
	s := newTestSystem(t)
	assertRules(t, runN(t, s, 101, 200, 0, 9), nil, RunNormal)
	assertRules(t, mustRun(t, s, 101, 200, 9), []RuleID{Rule5}, RunRejected)

	// 只有九点不触发。
	s = newTestSystem(t)
	assertRules(t, runN(t, s, 101, 200, 0, 9), nil, RunNormal)

	// 异侧打断：5 正 5 负不构成十点同侧。
	s = newTestSystem(t)
	runN(t, s, 101, 200, 0, 5)
	assertRules(t, runN(t, s, 99, 200, 5, 5), nil, RunNormal)

	// 零打断后重新计数十点。
	s = newTestSystem(t)
	runN(t, s, 101, 200, 0, 5)
	mustRun(t, s, 100, 200, 5)
	assertRules(t, runN(t, s, 101, 200, 6, 9), nil, RunNormal)
	assertRules(t, mustRun(t, s, 101, 200, 15), []RuleID{Rule5}, RunRejected)
}

// 同一次运行触发多条规则时，结果按规则一..五的次序列出。
func TestMultiRuleOrder(t *testing.T) {
	s := newTestSystem(t)
	// 前 9 次低水平 +21：第 2 次起触发规则二，第 4 次起触发规则四。
	assertRules(t, mustRun(t, s, 121, 200, 0), nil, RunWarning)
	assertRules(t, mustRun(t, s, 121, 200, 1), []RuleID{Rule2}, RunRejected)
	assertRules(t, mustRun(t, s, 121, 200, 2), []RuleID{Rule2}, RunRejected)
	assertRules(t, mustRun(t, s, 121, 200, 3), []RuleID{Rule2, Rule4}, RunRejected)
	runN(t, s, 121, 200, 4, 5)
	// 第 10 次：低 +31（超 3 倍）且高水平 -41（异侧超 2 倍），五条全部触发。
	assertRules(t, mustRun(t, s, 131, 159, 9),
		[]RuleID{Rule1, Rule2, Rule3, Rule4, Rule5}, RunRejected)
}

// 警告与正常的区分：恰等于 2 倍标准差为正常，差一为警告。
func TestWarningVsNormal(t *testing.T) {
	s := newTestSystem(t)
	assertRules(t, mustRun(t, s, 120, 200, 0), nil, RunNormal)
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 121, 200, 0), nil, RunWarning)
	// 警告不使项目失控。
	if st := projectState(t, s); st != StateInControl {
		t.Fatalf("warning run changed state to %v", st)
	}
	// 偏离量为零为正常。
	s = newTestSystem(t)
	assertRules(t, mustRun(t, s, 100, 200, 0), nil, RunNormal)
}

// 恢复需失控后连续两次正常运行；警告或失控运行都会清零重计。
func TestRecoveryRequiresTwoCleanRuns(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 0) // 正常，建立非失控运行时刻
	mustRun(t, s, 131, 200, 1) // 规则一，失控
	mustRun(t, s, 121, 200, 2) // 警告：不计入且清零
	mustRun(t, s, 100, 200, 3) // 正常 1
	mustRun(t, s, 131, 200, 4) // 再次失控：清零
	mustRun(t, s, 100, 200, 5) // 正常 1
	if st := projectState(t, s); st != StateOutOfControl {
		t.Fatalf("after 1 clean run state=%v, want OOC", st)
	}
	if err := issueErr(t, s, 6); err != ErrOutOfControl {
		t.Fatalf("issue during OOC: %v", err)
	}
	mustRun(t, s, 100, 200, 6) // 正常 2：恢复在此刻生效
	if st := projectState(t, s); st != StateInControl {
		t.Fatalf("after 2 clean runs state=%v, want in-control", st)
	}
	mustIssue(t, s, 6)
}

// 失控状态下的失控运行不改变状态，也不打断恢复计数的规则仅针对警告/失控。
func TestRejectedRunDuringOOCKeepsState(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 0)
	mustRun(t, s, 131, 200, 1) // 失控
	res := mustRun(t, s, 131, 200, 2)
	assertRules(t, res, []RuleID{Rule1, Rule2}, RunRejected)
	if st := projectState(t, s); st != StateOutOfControl {
		t.Fatalf("state=%v, want OOC", st)
	}
}

// 校准清空运行序列并使失控项目立即恢复在控，无需连续两次运行。
func TestCalibrate(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 0)
	mustRun(t, s, 131, 200, 1) // 失控
	if err := s.Calibrate("I1", "A1", 2); err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if st := projectState(t, s); st != StateInControl {
		t.Fatalf("after calibrate state=%v, want in-control", st)
	}
	// 校准不改变最近一次非失控运行时刻，有效期照常判定。
	mustIssue(t, s, 2)

	// 序列清零：校准前已有 3 个 +11，校准后再 3 个 +11 不触发规则四。
	s = newTestSystem(t)
	runN(t, s, 111, 200, 0, 3)
	if err := s.Calibrate("I1", "A1", 3); err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	assertRules(t, runN(t, s, 111, 200, 4, 3), nil, RunNormal)
	assertRules(t, mustRun(t, s, 111, 200, 7), []RuleID{Rule4}, RunRejected)

	// 校准不改变靶值与标准差：仍以原参数判定。
	s = newTestSystem(t)
	if err := s.Calibrate("I1", "A1", 0); err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	assertRules(t, mustRun(t, s, 131, 200, 1), []RuleID{Rule1}, RunRejected)
}

// 有效期：距最近一次非失控运行恰等于有效期仍可出具，晚一秒拒绝。
func TestValidityExactBoundary(t *testing.T) {
	s := newTestSystem(t) // 有效期 50
	mustRun(t, s, 100, 200, 10)
	mustIssue(t, s, 60) // 60-10=50，恰等，允许
	if err := issueErr(t, s, 61); err != ErrQCExpired {
		t.Fatalf("issue at 61: %v, want ErrQCExpired", err)
	}
	// 警告运行也算非失控运行，刷新有效期起点。
	mustRun(t, s, 121, 200, 70)
	mustIssue(t, s, 120)
}

// 失控追溯：标记区间为（上一次非失控运行时刻， 本次失控时刻]。
func TestRetroactiveMarkingBounds(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 5)
	r0 := mustIssue(t, s, 6)    // 早于上次非失控运行时刻：不标记
	mustRun(t, s, 100, 200, 10) // 最近一次非失控运行：t=10
	r1 := mustIssue(t, s, 10)   // 恰等于上次非失控运行时刻：不标记
	r2 := mustIssue(t, s, 15)   // 区间内：标记
	r3 := mustIssue(t, s, 20)   // 恰等于失控时刻：标记
	mustRun(t, s, 131, 200, 20)
	if st := reportStatus(t, s, r0); st != ReportIssued {
		t.Fatalf("r0 status=%v, want Issued", st)
	}
	if st := reportStatus(t, s, r1); st != ReportIssued {
		t.Fatalf("r1 status=%v, want Issued", st)
	}
	if st := reportStatus(t, s, r2); st != ReportPendingReview {
		t.Fatalf("r2 status=%v, want PendingReview", st)
	}
	if st := reportStatus(t, s, r3); st != ReportPendingReview {
		t.Fatalf("r3 status=%v, want PendingReview", st)
	}
}

// 从无非失控运行时，失控标记全部已出具报告（白盒构造该状态：
// 通过公开 API 无法在从无非失控运行的同时持有报告，故直接构造）。
func TestRetroactiveMarkingNoNonRejected(t *testing.T) {
	s := New()
	p := &project{
		low: levelParam{target: 100, sd: 10}, high: levelParam{target: 200, sd: 20},
		validity: 50, state: StateInControl,
	}
	for i, tm := range []int64{1, 2, 3} {
		id := int64(i + 1)
		s.reports[id] = &Report{ID: id, Analyte: "A1", Time: tm, Status: ReportIssued}
		node := &reportNode{id: id, time: tm}
		if p.tail == nil {
			p.head = node
		} else {
			p.tail.next = node
		}
		p.tail = node
	}
	p.markPendingReviewLocked(s) // 从无非失控运行：链上全部报告被标记
	if st := s.reports[1].Status; st != ReportPendingReview {
		t.Fatalf("report1=%v, want PendingReview", st)
	}
	if st := s.reports[2].Status; st != ReportPendingReview {
		t.Fatalf("report2=%v, want PendingReview", st)
	}
	if st := s.reports[3].Status; st != ReportPendingReview {
		t.Fatalf("report3=%v, want PendingReview", st)
	}
	if p.head != nil || p.tail != nil {
		t.Fatalf("markable list not cleared after marking")
	}
}

// 复核：待复核 -> 已复核；其他状态报状态不符。
func TestReviewFlow(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 0)
	r1 := mustIssue(t, s, 1)
	r2 := mustIssue(t, s, 2)
	mustRun(t, s, 131, 200, 3) // 失控，r1、r2 待复核
	if err := s.Review(r1, 4); err != nil {
		t.Fatalf("Review r1: %v", err)
	}
	if st := reportStatus(t, s, r1); st != ReportReviewed {
		t.Fatalf("r1=%v, want Reviewed", st)
	}
	if err := s.Review(r1, 5); err != ErrBadState {
		t.Fatalf("re-Review r1: %v, want ErrBadState", err)
	}
	// 恢复后新出具的报告为已出具状态，复核报状态不符。
	mustRun(t, s, 100, 200, 6)
	mustRun(t, s, 100, 200, 7)
	r3 := mustIssue(t, s, 8)
	if err := s.Review(r3, 9); err != ErrBadState {
		t.Fatalf("Review issued r3: %v, want ErrBadState", err)
	}
	if st := reportStatus(t, s, r2); st != ReportPendingReview {
		t.Fatalf("r2=%v, want PendingReview", st)
	}
}

// 错误优先级：参数非法 > 时钟回退 > 对象不存在 > 项目失控 > 从未质控 > 质控过期 > 状态不符。
func TestErrorPriority(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 100) // 接受 t=100，建立时钟

	// 参数非法优先于时钟回退：空标识 + 回退的 now。
	if _, err := s.Run("", "A1", 100, 200, 50); err != ErrInvalidParam {
		t.Fatalf("empty id + rollback: %v", err)
	}
	if _, err := s.Run("I1", "A1", 100, 200, -1); err != ErrInvalidParam {
		t.Fatalf("negative now: %v", err)
	}
	if _, err := s.Run("I1", "A1", 100, 200, 1_000_000_001); err != ErrInvalidParam {
		t.Fatalf("now > 1e9: %v", err)
	}
	// 时钟回退优先于对象不存在。
	if _, err := s.Run("I1", "NOPE", 100, 200, 50); err != ErrClockRollback {
		t.Fatalf("rollback + notfound: %v", err)
	}
	// 对象不存在。
	if _, err := s.Run("I1", "NOPE", 100, 200, 100); err != ErrNotFound {
		t.Fatalf("notfound: %v", err)
	}
	if err := s.Calibrate("I1", "NOPE", 100); err != ErrNotFound {
		t.Fatalf("calibrate notfound: %v", err)
	}
	// 项目失控优先于从未质控/质控过期。
	mustRun(t, s, 131, 200, 101) // 失控
	if err := issueErr(t, s, 1_000_000_000); err != ErrOutOfControl {
		t.Fatalf("OOC + expired: %v", err)
	}
	// 从未质控。
	s2 := newTestSystem(t)
	if err := issueErr(t, s2, 0); err != ErrNeverTested {
		t.Fatalf("never tested: %v", err)
	}
	// 质控过期。
	s3 := newTestSystem(t)
	mustRun(t, s3, 100, 200, 0)
	if err := issueErr(t, s3, 51); err != ErrQCExpired {
		t.Fatalf("expired: %v", err)
	}
	// 状态不符（复核已出具报告）与对象不存在（复核未知单号）。
	if err := s3.Review(999, 1); err != ErrNotFound {
		t.Fatalf("review notfound: %v", err)
	}
	r := mustIssue(t, s3, 50)
	if err := s3.Review(r, 50); err != ErrBadState {
		t.Fatalf("review issued: %v", err)
	}
	// 登记参数非法。
	if err := s3.RegisterAnalyte("I1", "A2", 100, 0, 200, 20, 50); err != ErrInvalidParam {
		t.Fatalf("zero sd: %v", err)
	}
	if err := s3.RegisterAnalyte("I1", "A2", 100, 10, 200, 20, 0); err != ErrInvalidParam {
		t.Fatalf("zero validity: %v", err)
	}
	if err := s3.RegisterAnalyte("", "A2", 100, 10, 200, 20, 50); err != ErrInvalidParam {
		t.Fatalf("empty instrument: %v", err)
	}
}

// 被拒绝的操作不改变任何状态与时钟。
func TestRejectedOpNoSideEffect(t *testing.T) {
	s := newTestSystem(t)
	mustRun(t, s, 100, 200, 100)
	// 时钟回退被拒绝，时钟停留在 100。
	if _, err := s.Run("I1", "A1", 131, 200, 99); err != ErrClockRollback {
		t.Fatalf("rollback: %v", err)
	}
	assertRules(t, mustRun(t, s, 100, 200, 100), nil, RunNormal)
	// 失控后出具被拒绝：不留记录、不推进时钟、不消耗单号。
	mustRun(t, s, 131, 200, 150)
	if err := issueErr(t, s, 160); err != ErrOutOfControl {
		t.Fatalf("issue during OOC: %v", err)
	}
	mustRun(t, s, 100, 200, 160) // 与拒绝操作同一时刻，时钟未被推进
	mustRun(t, s, 100, 200, 161) // 恢复
	id := mustIssue(t, s, 162)
	if id != 1 {
		t.Fatalf("first successful issue id=%d, want 1 (rejected issue left no record)", id)
	}
	// 状态不符的复核不推进时钟。
	if err := s.Review(id, 170); err != ErrBadState {
		t.Fatalf("review issued: %v", err)
	}
	mustIssue(t, s, 162)
}

// 相同操作序列重放得到完全相同的判定与报告标记。
func TestReplayDeterminism(t *testing.T) {
	runSeq := func() ([]RunResult, []ReportStatus) {
		s := newTestSystem(t)
		var results []RunResult
		vals := []int64{100, 121, 131, 100, 100, 111, 111, 111, 111, 100, 100}
		for i, v := range vals {
			res, err := s.Run("I1", "A1", v, 200, int64(i))
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
			results = append(results, res)
			if i == 2 || i == 6 {
				s.IssueReport("I1", "A1", int64(i))
			}
		}
		var statuses []ReportStatus
		for id := int64(1); id <= 2; id++ {
			st, err := s.ReportStatus(id)
			if err == nil {
				statuses = append(statuses, st)
			}
		}
		return results, statuses
	}
	r1, st1 := runSeq()
	r2, st2 := runSeq()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(st1, st2) {
		t.Fatalf("replay mismatch: %v/%v vs %v/%v", r1, st1, r2, st2)
	}
}

// 并发调用等价于某个串行顺序：失控期间不存在新出具的报告。
func TestConcurrentAccess(t *testing.T) {
	s := newTestSystem(t)
	done := make(chan struct{})
	const workers = 8
	const ops = 500
	errs := make(chan error, workers*ops)
	for w := 0; w < workers; w++ {
		go func(seed int64) {
			now := int64(0)
			for i := 0; i < ops; i++ {
				now++
				v := int64(100 + (i+int(seed))%45)
				if _, err := s.Run("I1", "A1", v, 200, now); err != nil && err != ErrClockRollback {
					errs <- err
				}
				if _, err := s.IssueReport("I1", "A1", now); err != nil &&
					err != ErrClockRollback && err != ErrOutOfControl && err != ErrQCExpired {
					errs <- err
				}
				if err := s.Calibrate("I1", "A1", now); err != nil && err != ErrClockRollback {
					errs <- err
				}
			}
			done <- struct{}{}
		}(int64(w))
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	close(errs)
	for err := range errs {
		t.Fatalf("unexpected error: %v", err)
	}
}
