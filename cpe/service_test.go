package cpe

import (
	"strconv"
	"sync/atomic"
	"testing"
)

var tickSeq int32

// tickTo 通过一个被接受的操作（注册全新持证人）把逻辑时钟推进到 now。
func tickTo(t *testing.T, s *Service, now int) {
	t.Helper()
	n := atomic.AddInt32(&tickSeq, 1)
	mustHolder(t, s, "$tick-"+strconv.Itoa(int(n)), 0, now)
}

func testCfg() Config {
	return Config{
		CycleLengthDays:      10,
		TotalRequired:        10,
		MandatoryMin:         3,
		MandatoryCap:         6,
		ElectiveCap:          4,
		GraceDays:            3,
		CarryoverCap:         2,
		CorrectionWindowDays: 5,
	}
}

func newTestService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustHolder(t *testing.T, s *Service, id string, issue, now int) {
	t.Helper()
	if err := s.RegisterHolder(id, issue, now); err != nil {
		t.Fatalf("RegisterHolder(%s): %v", id, err)
	}
}

func mustCredit(t *testing.T, s *Service, holder string, cat Category, credits, earned int, org string, now int) string {
	t.Helper()
	id, err := s.RegisterCredit(holder, cat, credits, earned, org, now)
	if err != nil {
		t.Fatalf("RegisterCredit(%s,%s,%d): %v", holder, cat, credits, err)
	}
	return id
}

func wantErr(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if ErrKindOf(err) != kind {
		t.Fatalf("want err kind %s, got %v", kind, err)
	}
}

func viewOf(t *testing.T, s *Service, holder string) View {
	t.Helper()
	v, err := s.GetAccounting(holder)
	if err != nil {
		t.Fatalf("GetAccounting(%s): %v", holder, err)
	}
	return v
}

func cycleOf(t *testing.T, v View, idx int) CycleView {
	t.Helper()
	for _, c := range v.Cycles {
		if c.Index == idx {
			return c
		}
	}
	t.Fatalf("cycle %d not found in view:\n%s", idx, v)
	return CycleView{}
}

// 周期末日取得的学分计入本周期，下周期首日取得的计入下一周期。
func TestCycleBoundaryLastDayVsFirstDay(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	// 周期0 [0,10)：凑到达标 M=3 E=4 O=3。
	mustCredit(t, s, "h", Mandatory, 3, 0, "orgA", 0)
	mustCredit(t, s, "h", Elective, 4, 1, "orgA", 1)
	mustCredit(t, s, "h", Online, 3, 2, "orgA", 2)
	// 周期末日（第9天）取得：计入周期0。
	mustCredit(t, s, "h", Mandatory, 1, 9, "orgA", 9)
	v := viewOf(t, s, "h")
	c0 := cycleOf(t, v, 0)
	if c0.Raw.Mandatory != 4 {
		t.Fatalf("cycle0 raw M = %d, want 4", c0.Raw.Mandatory)
	}
	// 下周期首日（第10天）取得：计入周期1。
	mustCredit(t, s, "h", Mandatory, 2, 10, "orgA", 10)
	v = viewOf(t, s, "h")
	c0 = cycleOf(t, v, 0)
	if !c0.Passed || c0.Phase != PhaseClosed {
		t.Fatalf("cycle0 should be closed&passed:\n%s", v)
	}
	if c0.Counted.Mandatory != 4 || c0.Counted.Total != 11 {
		t.Fatalf("cycle0 counted = %+v", c0.Counted)
	}
	c1 := cycleOf(t, v, 1)
	if c1.Start != 10 || c1.End != 20 {
		t.Fatalf("cycle1 range = [%d,%d)", c1.Start, c1.End)
	}
	if c1.Raw.Mandatory != 2 {
		t.Fatalf("cycle1 raw M = %d, want 2", c1.Raw.Mandatory)
	}
	// 结转：周期0 超额 1（M=4,E=4,O=3，非必修超额=7-(10-4)=1），上限2 → 结转1。
	if c0.CarryOut != 1 || c1.CarryIn != 1 {
		t.Fatalf("carryOut=%d carryIn=%d, want 1/1", c0.CarryOut, c1.CarryIn)
	}
	// 已关闭周期的取得日再登记：不计入任何周期。
	mustCredit(t, s, "h", Elective, 9, 5, "orgB", 11)
	v = viewOf(t, s, "h")
	if cycleOf(t, v, 1).Raw.Elective != 1 { // 仅结转的1
		t.Fatalf("late registration into closed cycle must not count:\n%s", v)
	}
}

// 各类封顶与线上联合约束同时生效。
func TestCapsAndOnlineJointConstraint(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 100, 0, "o", 0)
	mustCredit(t, s, "h", Elective, 100, 0, "o2", 0)
	mustCredit(t, s, "h", Online, 100, 0, "o3", 0)
	c0 := cycleOf(t, viewOf(t, s, "h"), 0)
	want := Counted{Mandatory: 6, Elective: 4, Online: 10, Total: 20}
	if c0.Counted != want {
		t.Fatalf("counted = %+v, want %+v", c0.Counted, want)
	}
	// 线上为0时联合约束不限制；必修为0时线上计入量被压到选修封顶量。
	s2 := newTestService(t, testCfg())
	mustHolder(t, s2, "h", 0, 0)
	mustCredit(t, s2, "h", Elective, 2, 0, "o", 0)
	mustCredit(t, s2, "h", Online, 50, 0, "o2", 0)
	c := cycleOf(t, viewOf(t, s2, "h"), 0)
	if c.Counted.Online != 2 || c.Counted.Total != 4 {
		t.Fatalf("online joint constraint: counted = %+v", c.Counted)
	}
}

// 必修不足而总量够：不达标 → 宽限 → 补必修后达标续期。
func TestMandatoryShortButTotalEnough(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 2, 0, "o", 0)
	mustCredit(t, s, "h", Elective, 4, 0, "o2", 0)
	mustCredit(t, s, "h", Online, 10, 0, "o3", 0) // 计入量被联合约束压到 2+4=6，总量12但必修2<3
	// 推进时钟到周期末：进入宽限期。
	tickTo(t, s, 10)
	v := viewOf(t, s, "h")
	c0 := cycleOf(t, v, 0)
	if c0.Phase != PhaseGrace || !c0.GraceEntered {
		t.Fatalf("cycle0 should be in grace:\n%s", v)
	}
	if c0.Counted.Total != 12 || c0.MeetsTotal != true || c0.MeetsMandatory != false || c0.Pass {
		t.Fatalf("unexpected evaluation: %+v", c0.Counted)
	}
	// 宽限期内补 1 分必修（取得日在宽限窗口内）→ 达标续期。
	mustCredit(t, s, "h", Mandatory, 1, 11, "o", 11)
	v = viewOf(t, s, "h")
	c0 = cycleOf(t, v, 0)
	if !c0.Passed || !c0.PassedInGrace {
		t.Fatalf("cycle0 should pass in grace:\n%s", v)
	}
	if c0.CarryOut != 0 {
		t.Fatalf("grace-passed cycle must not carry over, got %d", c0.CarryOut)
	}
	if cycleOf(t, v, 1).CarryIn != 0 {
		t.Fatalf("cycle1 carryIn must be 0")
	}
}

// 宽限期末日恰等（计入）与晚一日（证书失效）。
func TestGraceLastDayExactVsOneDayLate(t *testing.T) {
	setup := func() *Service {
		s := newTestService(t, testCfg()) // 周期[0,10)，宽限[10,13)
		mustHolder(t, s, "h", 0, 0)
		mustCredit(t, s, "h", Mandatory, 3, 0, "o", 0)
		mustCredit(t, s, "h", Elective, 4, 0, "o2", 0)
		mustCredit(t, s, "h", Online, 2, 0, "o3", 0) // 总量9，差1
		return s
	}
	// 恰在宽限最后一日（now=12）补登取得日为12的学分 → 计入并达标。
	s1 := setup()
	mustCredit(t, s1, "h", Online, 1, 12, "o3", 12)
	v := viewOf(t, s1, "h")
	if !cycleOf(t, v, 0).PassedInGrace || v.Cert != CertActive {
		t.Fatalf("should pass on last grace day:\n%s", v)
	}
	// 晚一日（now=13）：宽限结束时刻之后，证书失效，操作被拒。
	s2 := setup()
	_, err := s2.RegisterCredit("h", Online, 1, 12, "o3", 13)
	wantErr(t, err, ErrCertificateExpired)
	tickTo(t, s2, 13) // 被拒绝的操作不推进时钟；用被接受的操作推进
	v = viewOf(t, s2, "h")
	if v.Cert != CertExpired {
		t.Fatalf("cert should be expired:\n%s", v)
	}
}

// 宽限期结束时刻之后补登的、取得日属于宽限期内的学分一律不计入。
func TestGraceWindowEarnedRegisteredAfterGraceEnd(t *testing.T) {
	s := newTestService(t, testCfg()) // 周期0 [0,10)，宽限窗口 [10,13)
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 3, 0, "o", 0)
	mustCredit(t, s, "h", Elective, 4, 0, "o2", 0)
	mustCredit(t, s, "h", Online, 2, 0, "o3", 0) // 差1进入宽限
	// 宽限期内（now=11）补足 → 续期，开启周期1 [10,20)。
	mustCredit(t, s, "h", Online, 1, 11, "o3", 11)
	v := viewOf(t, s, "h")
	if !cycleOf(t, v, 0).PassedInGrace {
		t.Fatalf("cycle0 should pass in grace:\n%s", v)
	}
	// now=13（宽限结束时刻之后）补登取得日=12（属于宽限窗口）的学分 → 不计入周期1。
	mustCredit(t, s, "h", Online, 7, 12, "o4", 13)
	v = viewOf(t, s, "h")
	if c1 := cycleOf(t, v, 1); c1.Raw.Online != 0 {
		t.Fatalf("credit earned in grace window registered after grace end must not count:\n%s", v)
	}
	// 对照：now=12（宽限结束之前）登记取得日=11 的学分，计入周期1。
	s2 := newTestService(t, testCfg())
	mustHolder(t, s2, "h", 0, 0)
	mustCredit(t, s2, "h", Mandatory, 3, 0, "o", 0)
	mustCredit(t, s2, "h", Elective, 4, 0, "o2", 0)
	mustCredit(t, s2, "h", Online, 2, 0, "o3", 0)
	mustCredit(t, s2, "h", Online, 1, 11, "o3", 11) // 宽限内补足
	mustCredit(t, s2, "h", Online, 7, 11, "o4", 12) // 周期1 内登记，取得日属于周期1区间
	if c1 := cycleOf(t, viewOf(t, s2, "h"), 1); c1.Raw.Online != 7 {
		t.Fatalf("credit earned on cycle1 day should count, got %d", c1.Raw.Online)
	}
}

// 更正期限恰等于固定天数仍允许，超过一日拒绝。
func TestCorrectionWindowExactBoundary(t *testing.T) {
	s := newTestService(t, testCfg()) // CorrectionWindowDays = 5
	mustHolder(t, s, "h", 0, 0)
	rec := mustCredit(t, s, "h", Mandatory, 4, 0, "o", 2) // 登记于 now=2
	// now=7：7-2=5，恰等于窗口，允许。
	if err := s.CorrectCredit("h", rec, "o", 6, 7); err != nil {
		t.Fatalf("correction at exactly window must be allowed: %v", err)
	}
	if c0 := cycleOf(t, viewOf(t, s, "h"), 0); c0.Raw.Mandatory != 6 {
		t.Fatalf("correction not applied, raw M = %d", c0.Raw.Mandatory)
	}
	// now=8：8-2=6，超出窗口，拒绝。
	wantErr(t, s.CorrectCredit("h", rec, "o", 7, 8), ErrCorrectionWindowExpired)
	// 撤销同样受窗口约束。
	wantErr(t, s.RevokeCredit("h", rec, "o", 8), ErrCorrectionWindowExpired)
	// 被拒操作未改变状态。
	if c0 := cycleOf(t, viewOf(t, s, "h"), 0); c0.Raw.Mandatory != 6 {
		t.Fatalf("rejected correction must not change state, raw M = %d", c0.Raw.Mandatory)
	}
}

// 更正/撤销后的核算结果与一开始就是更正后的值完全一致。
func TestCorrectionEquivalenceWithDirectRegistration(t *testing.T) {
	cfg := testCfg()
	// A：登记 10 后更正为 6。
	sa := newTestService(t, cfg)
	mustHolder(t, sa, "h", 0, 0)
	rec := mustCredit(t, sa, "h", Mandatory, 10, 0, "o", 0)
	if err := sa.CorrectCredit("h", rec, "o", 6, 1); err != nil {
		t.Fatalf("correct: %v", err)
	}
	mustCredit(t, sa, "h", Elective, 3, 1, "o", 1)
	// B：直接登记 6。
	sb := newTestService(t, cfg)
	mustHolder(t, sb, "h", 0, 0)
	mustCredit(t, sb, "h", Mandatory, 6, 0, "o", 0)
	mustCredit(t, sb, "h", Elective, 3, 1, "o", 1)
	va, vb := viewOf(t, sa, "h"), viewOf(t, sb, "h")
	if !viewsEqual(va, vb) {
		t.Fatalf("correction equivalence violated:\nA:%s\nB:%s", va, vb)
	}
	// A：登记 10 与 5 后撤销 10；B：仅登记 5。
	sc := newTestService(t, cfg)
	mustHolder(t, sc, "h", 0, 0)
	r1 := mustCredit(t, sc, "h", Online, 10, 0, "o", 0)
	mustCredit(t, sc, "h", Online, 5, 1, "o", 1)
	if err := sc.RevokeCredit("h", r1, "o", 2); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sd := newTestService(t, cfg)
	mustHolder(t, sd, "h", 0, 0)
	mustCredit(t, sd, "h", Online, 5, 0, "o", 0)
	vc, vd := viewOf(t, sc, "h"), viewOf(t, sd, "h")
	// 注意：sc 中第二条记录取得日为1，sd 中为0；同属周期0，核算应一致。
	if !viewsEqual(vc, vd) {
		t.Fatalf("revocation equivalence violated:\nC:%s\nD:%s", vc, vd)
	}
}

func viewsEqual(a, b View) bool {
	if a.HolderID != b.HolderID || a.Generation != b.Generation || a.Cert != b.Cert ||
		len(a.Cycles) != len(b.Cycles) {
		return false
	}
	for i := range a.Cycles {
		if a.Cycles[i] != b.Cycles[i] {
			return false
		}
	}
	return true
}

// 结转构成：仅选修与线上超额可结转，必修超额不结转，且受结转上限约束；
// 结转在下一周期按选修计入并受选修上限约束。
func TestCarryoverCompositionAndCap(t *testing.T) {
	cfg := Config{
		CycleLengthDays:      10,
		TotalRequired:        5,
		MandatoryMin:         3,
		MandatoryCap:         6,
		ElectiveCap:          4,
		GraceDays:            0,
		CarryoverCap:         3,
		CorrectionWindowDays: 5,
	}
	// 必修超额不结转：M=6 E=0 O=0，总量6超1，但非必修超额为0。
	s1 := newTestService(t, cfg)
	mustHolder(t, s1, "h", 0, 0)
	mustCredit(t, s1, "h", Mandatory, 6, 0, "o", 0)
	tickTo(t, s1, 10)
	v := viewOf(t, s1, "h")
	if c0 := cycleOf(t, v, 0); !c0.Passed || c0.CarryOut != 0 {
		t.Fatalf("mandatory excess must not carry: %+v", c0)
	}
	// 选修+线上超额结转，受上限3约束：M=3 E=4 O=5，总量12超7，非必修超额=9-(5-3)=7 → 结转3。
	s2 := newTestService(t, cfg)
	mustHolder(t, s2, "h", 0, 0)
	mustCredit(t, s2, "h", Mandatory, 3, 0, "o", 0)
	mustCredit(t, s2, "h", Elective, 4, 0, "o2", 0)
	mustCredit(t, s2, "h", Online, 5, 0, "o3", 0)
	tickTo(t, s2, 10)
	v = viewOf(t, s2, "h")
	c0 := cycleOf(t, v, 0)
	if !c0.Passed || c0.CarryOut != 3 {
		t.Fatalf("carryOut = %d, want 3 (capped): %+v", c0.CarryOut, c0)
	}
	c1 := cycleOf(t, v, 1)
	if c1.CarryIn != 3 || c1.Raw.Elective != 3 {
		t.Fatalf("carryIn must count as elective raw: %+v", c1)
	}
	// 结转计入量同样受选修上限约束：再登记4分选修，选修原始=7，封顶4。
	mustCredit(t, s2, "h", Elective, 4, 10, "o2", 10)
	c1 = cycleOf(t, viewOf(t, s2, "h"), 1)
	if c1.Raw.Elective != 7 || c1.Counted.Elective != 4 {
		t.Fatalf("carried credits must be subject to elective cap: %+v", c1)
	}
}

// 宽限期届满仍未达标 → 证书失效；失效后只能重新注册为新持证人，原学分不继承。
func TestExpiryAndReregister(t *testing.T) {
	cfg := testCfg()
	cfg.GraceDays = 0 // 无宽限：周期末未达标即失效
	s := newTestService(t, cfg)
	mustHolder(t, s, "h", 0, 0)
	rec := mustCredit(t, s, "h", Mandatory, 1, 8, "o", 8)
	// 周期末未达标 → 失效；之后的登记/更正/撤销均被拒（证书已失效）。
	_, err := s.RegisterCredit("h", Elective, 1, 1, "o", 10)
	wantErr(t, err, ErrCertificateExpired)
	tickTo(t, s, 10)
	// 更正窗口未超（10-8=2<=5），故报证书已失效。
	wantErr(t, s.CorrectCredit("h", rec, "o", 2, 10), ErrCertificateExpired)
	wantErr(t, s.RevokeCredit("h", rec, "o", 10), ErrCertificateExpired)
	v := viewOf(t, s, "h")
	if v.Cert != CertExpired || cycleOf(t, v, 0).Passed {
		t.Fatalf("cert should be expired:\n%s", v)
	}
	// 重新注册为新持证人（同一 ID、新一代）。
	mustHolder(t, s, "h", 20, 20)
	v = viewOf(t, s, "h")
	if v.Generation != 1 || v.Cert != CertActive {
		t.Fatalf("re-register should start generation 1 active:\n%s", v)
	}
	// 原学分不继承。
	if c0 := cycleOf(t, v, 0); c0.Raw.Mandatory != 0 || c0.Start != 20 {
		t.Fatalf("old credits must not be inherited:\n%s", v)
	}
	// 旧记录 ID 不再存在。
	wantErr(t, s.CorrectCredit("h", rec, "o", 2, 21), ErrNotFound)
	// 新周期可正常登记。
	mustCredit(t, s, "h", Mandatory, 5, 20, "o", 20)
	if c0 := cycleOf(t, viewOf(t, s, "h"), 0); c0.Raw.Mandatory != 5 {
		t.Fatalf("new generation cycle0 raw M = %d, want 5", c0.Raw.Mandatory)
	}
	// 历史查询仍可见旧一代的实际状态。
	hv, err := s.GetAccountingAt("h", 9)
	if err != nil {
		t.Fatalf("GetAccountingAt: %v", err)
	}
	if hv.Generation != 0 || cycleOf(t, hv, 0).Raw.Mandatory != 1 {
		t.Fatalf("historical view should show generation 0:\n%s", hv)
	}
}

// 重复登记判定：同机构+同取得日+同类别的未撤销记录。
func TestDuplicateRegistration(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 3, 0, "orgA", 0)
	// 完全同键 → 重复拒绝。
	_, err := s.RegisterCredit("h", Mandatory, 5, 0, "orgA", 0)
	wantErr(t, err, ErrDuplicate)
	// 不同机构 / 不同取得日 / 不同类别 → 允许。
	mustCredit(t, s, "h", Mandatory, 5, 0, "orgB", 0)
	mustCredit(t, s, "h", Elective, 5, 0, "orgA", 0)
	mustCredit(t, s, "h", Mandatory, 5, 1, "orgA", 1)
	// 撤销后可重新登记同键。
	rec := mustCredit(t, s, "h", Online, 2, 2, "orgA", 2)
	if err := s.RevokeCredit("h", rec, "orgA", 3); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	mustCredit(t, s, "h", Online, 2, 2, "orgA", 4)
}

// 时钟回退：now 小于上一次被接受操作的 now 时拒绝，且不改变任何状态。
func TestClockRollback(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 3, 0, "o", 5)
	before := viewOf(t, s, "h")
	// now=4 < 5 → 时钟回退。
	_, err := s.RegisterCredit("h", Elective, 2, 0, "o", 4)
	wantErr(t, err, ErrClockRollback)
	wantErr(t, s.RegisterHolder("h2", 0, 4), ErrClockRollback)
	// 状态与时钟均未改变：now=5 的操作仍可被接受。
	mustCredit(t, s, "h", Elective, 2, 0, "o", 5)
	after := viewOf(t, s, "h")
	if cycleOf(t, after, 0).Raw.Elective != 2 {
		t.Fatalf("state changed unexpectedly:\nbefore:%s\nafter:%s", before, after)
	}
	// 取得日晚于 now → 参数非法。
	_, err = s.RegisterCredit("h", Online, 1, 6, "o", 5)
	wantErr(t, err, ErrInvalidParam)
}

// 错误优先级：多重违规时只报优先级最高的第一个。
func TestErrorPriority(t *testing.T) {
	cfg := testCfg()
	cfg.GraceDays = 0
	s := newTestService(t, cfg)
	mustHolder(t, s, "h", 0, 0)
	rec := mustCredit(t, s, "h", Mandatory, 1, 0, "o", 0)
	tickTo(t, s, 10) // 周期0未达标且无宽限 → 证书失效（lastNow=10）

	// 参数非法 优先于 时钟回退。
	_, err := s.RegisterCredit("h", Category(9), 1, 0, "o", 5)
	wantErr(t, err, ErrInvalidParam)
	// 时钟回退 优先于 不存在。
	_, err = s.RegisterCredit("ghost", Mandatory, 1, 0, "o", 5)
	wantErr(t, err, ErrClockRollback)
	// 不存在 优先于 状态不允许/重复/超期/失效。
	wantErr(t, s.CorrectCredit("ghost", rec, "o", 2, 10), ErrNotFound)
	wantErr(t, s.CorrectCredit("h", "R99999999", "o", 2, 10), ErrNotFound)
	// 状态不允许（非出具机构）优先于 超出更正期限 与 证书已失效。
	wantErr(t, s.CorrectCredit("h", rec, "other-org", 2, 10), ErrStateNotAllowed)
	// 重复登记 优先于 证书已失效。
	_, err = s.RegisterCredit("h", Mandatory, 9, 0, "o", 10)
	wantErr(t, err, ErrDuplicate)
	// 超出更正期限 优先于 证书已失效（10-0=10 > 5）。
	wantErr(t, s.CorrectCredit("h", rec, "o", 2, 10), ErrCorrectionWindowExpired)
	// 仅证书失效时 → 证书已失效。
	_, err = s.RegisterCredit("h", Elective, 1, 1, "o", 10)
	wantErr(t, err, ErrCertificateExpired)
}

// 历史查询：任意历史时刻的核算结果与当时实际一致。
func TestHistoricalQuery(t *testing.T) {
	s := newTestService(t, testCfg())
	mustHolder(t, s, "h", 0, 0)
	mustCredit(t, s, "h", Mandatory, 3, 0, "o", 1)
	mustCredit(t, s, "h", Elective, 4, 1, "o", 2)
	mustCredit(t, s, "h", Online, 3, 2, "o", 3)
	tickTo(t, s, 10) // 关闭周期0（达标）
	mustCredit(t, s, "h", Mandatory, 1, 10, "o", 11)

	cases := []struct {
		asOf   int
		rawM   int
		rawE   int
		rawO   int
		nCycle int
	}{
		{0, 0, 0, 0, 1},
		{1, 3, 0, 0, 1},
		{2, 3, 4, 0, 1},
		{3, 3, 4, 3, 1},
		{9, 3, 4, 3, 1},  // 周期0尚未关闭（时钟未越过末日）
		{10, 3, 4, 3, 2}, // 周期0已关闭，周期1开启
		{11, 3, 4, 3, 2},
	}
	for _, tc := range cases {
		v, err := s.GetAccountingAt("h", tc.asOf)
		if err != nil {
			t.Fatalf("GetAccountingAt(%d): %v", tc.asOf, err)
		}
		if len(v.Cycles) != tc.nCycle {
			t.Fatalf("asOf=%d: %d cycles, want %d\n%s", tc.asOf, len(v.Cycles), tc.nCycle, v)
		}
		c0 := cycleOf(t, v, 0)
		if c0.Raw.Mandatory != tc.rawM || c0.Raw.Elective != tc.rawE || c0.Raw.Online != tc.rawO {
			t.Fatalf("asOf=%d: raw=%+v, want M%d E%d O%d", tc.asOf, c0.Raw, tc.rawM, tc.rawE, tc.rawO)
		}
	}
	// 周期0在 asOf>=10 的视图中为已关闭且达标。
	v, _ := s.GetAccountingAt("h", 10)
	if c0 := cycleOf(t, v, 0); !c0.Passed || c0.Phase != PhaseClosed {
		t.Fatalf("asOf=10 cycle0 should be closed&passed:\n%s", v)
	}
	// asOf 超过当前时钟 → 参数非法；持证人尚不存在的时刻 → 不存在。
	if _, err := s.GetAccountingAt("h", 100); ErrKindOf(err) != ErrInvalidParam {
		t.Fatalf("want invalid param, got %v", err)
	}
	mustHolder(t, s, "late", 11, 11)
	if _, err := s.GetAccountingAt("late", 5); ErrKindOf(err) != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}
