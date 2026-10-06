package hd

import "testing"

type baySpec struct {
	id  string
	z   Zone
	obs bool
}

type patSpec struct {
	id  string
	inf Infection
}

func testSystem(t *testing.T) *System {
	t.Helper()
	s, err := New(Config{CleanNegative: 30, CleanHBV: 40, CleanHCV: 50, DeepClean: 120, MinRecovery: 60})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func regBays(t *testing.T, s *System, now int, bays ...baySpec) {
	t.Helper()
	for _, b := range bays {
		if err := s.RegisterBay(now, b.id, b.z, b.obs, true); err != nil {
			t.Fatalf("register bay %s: %v", b.id, err)
		}
	}
}

func regPats(t *testing.T, s *System, now int, ps ...patSpec) {
	t.Helper()
	for _, p := range ps {
		if err := s.RegisterPatient(now, p.id, p.inf); err != nil {
			t.Fatalf("register patient %s: %v", p.id, err)
		}
	}
}

func code(err error) ErrorCode {
	if err == nil {
		return -1
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -998
}

func mustBook(t *testing.T, s *System, now int, id, pid string, start, dur int) string {
	t.Helper()
	bay, err := s.BookTreatment(now, id, pid, start, dur)
	if err != nil {
		t.Fatalf("book %s: %v", id, err)
	}
	return bay
}

// 消毒边界：结束+消毒恰等于下次开始允许；早一分钟拒绝。
func TestCleanBoundary(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0, baySpec{"G1", ZoneGeneral, false})
	regPats(t, s, 0, patSpec{"p", InfectionNegative}, patSpec{"q", InfectionNegative})

	mustBook(t, s, 1, "t1", "p", 100, 200) // end 300, clean to 330
	if _, err := s.BookTreatment(2, "t2", "q", 330, 100); err != nil {
		t.Fatalf("exact clean boundary must be allowed: %v", err)
	}
	mustBook(t, s, 3, "t3", "q", 600, 10) // end 610
	if _, err := s.BookTreatment(4, "t4", "p", 639, 10); code(err) != ErrNoFeasibleBay {
		t.Fatalf("one minute early must be rejected, got %v", err)
	}
	if _, err := s.BookTreatment(5, "t4", "p", 640, 10); err != nil {
		t.Fatalf("exact boundary must be allowed: %v", err)
	}
}

// 患者最短恢复间隔：end+60 允许，早一分钟拒绝。
func TestPatientRecovery(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s, 0, patSpec{"p", InfectionNegative})
	mustBook(t, s, 1, "t1", "p", 0, 100) // end 100, recover to 160
	if _, err := s.BookTreatment(2, "t2", "p", 159, 10); code(err) != ErrPatientConflict {
		t.Fatalf("recovery too soon must be patient conflict, got %v", err)
	}
	if _, err := s.BookTreatment(3, "t2", "p", 160, 10); err != nil {
		t.Fatalf("exact recovery boundary must be allowed: %v", err)
	}
}

// 乙肝/丙肝相邻：类型不同必须间隔深度消毒；同类型只需常规消毒。
func TestDeepCleanHBVHCV(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0, baySpec{"I1", ZoneIsolation, false})
	regPats(t, s, 0, patSpec{"h", InfectionHBV}, patSpec{"c", InfectionHCV}, patSpec{"c2", InfectionHCV})
	mustBook(t, s, 1, "h1", "h", 0, 100) // end 100, deep -> 220
	if _, err := s.BookTreatment(2, "c1", "c", 219, 10); code(err) != ErrNoFeasibleBay {
		t.Fatalf("HCV at 219 must trigger deep clean rejection, got %v", err)
	}
	if _, err := s.BookTreatment(3, "c1", "c", 220, 10); err != nil {
		t.Fatalf("HCV at exactly 220 must be allowed: %v", err)
	}
	// c1 end 230；同为 HCV 只需常规消毒 50 -> 280。
	if _, err := s.BookTreatment(4, "c2a", "c2", 279, 1); code(err) != ErrNoFeasibleBay {
		t.Fatalf("same type at 279 still within normal clean, got %v", err)
	}
	if _, err := s.BookTreatment(5, "c2a", "c2", 280, 1); err != nil {
		t.Fatalf("same type at 280 must be allowed: %v", err)
	}
}

// 区域/观察位资格：阳性只去隔离区、阴性只去普通区、待定只去观察位。
func TestIsolationEligibility(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, true},
		baySpec{"I1", ZoneIsolation, false},
	)
	regPats(t, s, 0,
		patSpec{"neg", InfectionNegative},
		patSpec{"h", InfectionHBV},
		patSpec{"pend", InfectionPending},
	)
	if b := mustBook(t, s, 1, "h1", "h", 0, 10); b != "I1" {
		t.Fatalf("HBV must use isolation, got %s", b)
	}
	if b := mustBook(t, s, 2, "n1", "neg", 0, 10); b != "G1" {
		t.Fatalf("negative must use smallest general bay, got %s", b)
	}
	if b := mustBook(t, s, 3, "u1", "pend", 0, 10); b != "G2" {
		t.Fatalf("pending must use observed bay, got %s", b)
	}

	// 全中心没有区域合格机位 -> 感染隔离冲突。
	s2 := testSystem(t)
	regBays(t, s2, 0, baySpec{"G1", ZoneGeneral, false})
	regPats(t, s2, 0, patSpec{"h", InfectionHBV})
	if _, err := s2.BookTreatment(1, "h1", "h", 0, 10); code(err) != ErrIsolationConflict {
		t.Fatalf("expected isolation conflict, got %v", err)
	}

	// 待定患者在有观察位但被占用时属于无可行机位（非隔离冲突）。
	s3 := testSystem(t)
	regBays(t, s3, 0, baySpec{"G2", ZoneGeneral, true})
	regPats(t, s3, 0, patSpec{"u", InfectionPending}, patSpec{"v", InfectionPending})
	mustBook(t, s3, 1, "u1", "u", 0, 100)
	if _, err := s3.BookTreatment(2, "v1", "v", 0, 100); code(err) != ErrNoFeasibleBay {
		t.Fatalf("busy observed bay must be no-feasible, got %v", err)
	}
}

// 周期方案：同机位优先、贪心分流、全有或全无。
func TestPlanAllocation(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s, 0,
		patSpec{"p", InfectionNegative},
		patSpec{"q", InfectionNegative},
		patSpec{"r", InfectionNegative},
	)

	days := map[int]bool{0: true, 2: true}
	bays, err := s.ApplyPlan(1, "plan1", "p", days, 600, 60, 0, 6*minutesPerDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(bays) != 2 || bays[0] != "G1" || bays[1] != "G1" {
		t.Fatalf("same-bay priority failed: %v", bays)
	}

	// 贪心分流：周一 700 仅 G1 被占，周四 700 两机位均被占 =>
	// 同机位方案均不可行，逐次贪心得到 [G2, G1]。
	monday := minutesPerDay + 700
	thursday := 4*minutesPerDay + 700
	mustBook(t, s, 2, "blk-mon", "r", monday, 60) // G1 周一占
	// 周四只保留 G2 被占：先由 q 占 G1、r 占 G2，再取消 q（now 早于治疗开始）。
	mustBook(t, s, 3, "blk-thu1", "q", thursday, 60)
	mustBook(t, s, 4, "blk-thu2", "r", thursday, 60)
	if err := s.CancelTreatment(5, "blk-thu1"); err != nil {
		t.Fatal(err)
	}
	bays2, err := s.ApplyPlan(6, "plan2", "p", map[int]bool{1: true, 4: true}, 700, 60, 0, 6*minutesPerDay)
	if err != nil {
		t.Fatal(err)
	}
	if bays2[0] != "G2" || bays2[1] != "G1" {
		t.Fatalf("greedy split failed: %v", bays2)
	}

	// 全有或全无：全新系统，周二 700 两机位均占 => 方案拒绝且不留记录。
	s2 := testSystem(t)
	regBays(t, s2, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s2, 0,
		patSpec{"p", InfectionNegative},
		patSpec{"q", InfectionNegative},
		patSpec{"r", InfectionNegative},
	)
	start := 2*minutesPerDay + 700
	mustBook(t, s2, 1, "x1", "q", start, 60)
	mustBook(t, s2, 2, "x2", "r", start, 60)
	_, err = s2.ApplyPlan(3, "plan3", "p", map[int]bool{2: true, 5: true}, 700, 60,
		0, 13*minutesPerDay)
	if code(err) != ErrNoFeasibleBay {
		t.Fatalf("all-or-nothing rejection expected, got %v", err)
	}
	if _, ok := s2.Treatment("plan3#0"); ok {
		t.Fatal("rejected plan must leave no treatments")
	}
}

// 故障：成功改派、进行中治疗保留、无法改派时整体拒绝且原状态不变。
func TestFaultReassign(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s, 0, patSpec{"p", InfectionNegative})
	mustBook(t, s, 1, "future", "p", 4000, 100)
	mustBook(t, s, 2, "ongoing", "p", 2000, 200) // 3000 结束
	if err := s.ReportFault(3, "G1", 2050); err != nil {
		t.Fatalf("fault with feasible reassign: %v", err)
	}
	if tr, _ := s.Treatment("future"); tr.BayID != "G2" {
		t.Fatalf("future treatment must move to G2, got %s", tr.BayID)
	}
	if tr, _ := s.Treatment("ongoing"); tr.BayID != "G1" {
		t.Fatalf("ongoing treatment must remain on G1, got %s", tr.BayID)
	}

	s2 := testSystem(t)
	regBays(t, s2, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s2, 0, patSpec{"p", InfectionNegative}, patSpec{"q", InfectionNegative})
	mustBook(t, s2, 1, "future", "p", 1000, 100)
	mustBook(t, s2, 2, "block", "q", 1000, 100)
	if err := s2.ReportFault(3, "G1", 500); code(err) != ErrNoFeasibleBay {
		t.Fatalf("fault must be rejected when reassign impossible, got %v", err)
	}
	if tr, _ := s2.Treatment("future"); tr.BayID != "G1" {
		t.Fatalf("rejected fault must restore original bay, got %s", tr.BayID)
	}
}

// 恢复可用不回调；恢复后新机位选择恢复正常。
func TestRecoverNoCallback(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s, 0, patSpec{"p", InfectionNegative})
	mustBook(t, s, 1, "future", "p", 1000, 100)
	if err := s.ReportFault(2, "G1", 500); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverBay(3, "G1", 900); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s.Treatment("future"); tr.BayID != "G2" {
		t.Fatalf("recovery must not call back treatment, got %s", tr.BayID)
	}
	if _, err := s.BookTreatment(4, "late", "p", 2000, 10); err != nil {
		t.Fatalf("after recovery G1 should accept new treatments: %v", err)
	}
}

// 待定 -> 阳性：原观察位不再合法，改派隔离机位；失败整体拒绝。
func TestInfectionChange(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, true},
		baySpec{"I1", ZoneIsolation, false},
	)
	regPats(t, s, 0, patSpec{"u", InfectionPending}, patSpec{"h", InfectionHBV})
	mustBook(t, s, 1, "u1", "u", 1000, 100)
	if err := s.ChangeInfection(2, "u", InfectionHBV, 500); err != nil {
		t.Fatalf("change pending->HBV with free isolation bay: %v", err)
	}
	if tr, _ := s.Treatment("u1"); tr.BayID != "I1" || tr.Infection != InfectionHBV {
		t.Fatalf("u1 must move to I1 and snapshot HBV, got %s/%s", tr.BayID, tr.Infection)
	}
	if inf, _ := s.PatientInfection("u"); inf != InfectionHBV {
		t.Fatalf("patient state must update")
	}

	// 隔离机位同时段被占 => 状态变更拒绝，状态与分配保持原样。
	s2 := testSystem(t)
	regBays(t, s2, 0,
		baySpec{"G2", ZoneGeneral, true},
		baySpec{"I1", ZoneIsolation, false},
	)
	regPats(t, s2, 0, patSpec{"u", InfectionPending}, patSpec{"h", InfectionHBV})
	mustBook(t, s2, 1, "u1", "u", 1000, 100)
	mustBook(t, s2, 2, "h1", "h", 1000, 100)
	if err := s2.ChangeInfection(3, "u", InfectionHBV, 500); code(err) != ErrNoFeasibleBay {
		t.Fatalf("blocked isolation must reject change, got %v", err)
	}
	if tr, _ := s2.Treatment("u1"); tr.BayID != "G2" || tr.Infection != InfectionPending {
		t.Fatalf("rejected change must restore, got %s/%s", tr.BayID, tr.Infection)
	}
	if inf, _ := s2.PatientInfection("u"); inf != InfectionPending {
		t.Fatal("rejected change must not update patient state")
	}

	// 阴性转阳性：原普通机位若仍可行（此处不合法）则改派；已开始治疗不追溯。
	s3 := testSystem(t)
	regBays(t, s3, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"I1", ZoneIsolation, false},
	)
	regPats(t, s3, 0, patSpec{"p", InfectionNegative})
	mustBook(t, s3, 1, "done", "p", 100, 100) // changeTime 之后但 now 之前
	mustBook(t, s3, 2, "future", "p", 1000, 100)
	if err := s3.ChangeInfection(500, "p", InfectionHBV, 400); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s3.Treatment("done"); tr.BayID != "G1" || tr.Infection != InfectionNegative {
		t.Fatalf("started treatment must not be revisited, got %s/%s", tr.BayID, tr.Infection)
	}
	if tr, _ := s3.Treatment("future"); tr.BayID != "I1" {
		t.Fatalf("future treatment must move, got %s", tr.BayID)
	}
}

// 取消单次治疗释放消毒占用；已开始不可取消；取消不触发他人改派。
func TestCancelReleasesClean(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0, baySpec{"G1", ZoneGeneral, false})
	regPats(t, s, 0, patSpec{"p", InfectionNegative}, patSpec{"q", InfectionNegative})
	mustBook(t, s, 1, "t1", "p", 100, 100) // end 200, clean to 230
	if _, err := s.BookTreatment(2, "t2", "q", 220, 10); code(err) != ErrNoFeasibleBay {
		t.Fatalf("within clean window must reject, got %v", err)
	}
	if err := s.CancelTreatment(3, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BookTreatment(4, "t2", "q", 220, 10); err != nil {
		t.Fatalf("after cancel, clean occupancy must be released: %v", err)
	}
	// 已开始治疗不可取消。
	if err := s.CancelTreatment(500, "t2"); code(err) != ErrInvalidState {
		t.Fatalf("started treatment cannot be canceled, got %v", err)
	}
	if _, ok := s.Treatment("t1"); ok {
		t.Fatal("canceled treatment should disappear from active records")
	}
}

// 取消整个方案：成功则全部次数与消毒释放；有已开始次数则拒绝。
func TestCancelPlan(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0, baySpec{"G1", ZoneGeneral, false})
	regPats(t, s, 0, patSpec{"p", InfectionNegative}, patSpec{"q", InfectionNegative})
	days := map[int]bool{0: true, 3: true}
	if _, err := s.ApplyPlan(1, "plan1", "p", days, 600, 60, 0, 6*minutesPerDay); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPlan(2, "plan1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Treatment("plan1#0"); ok {
		t.Fatal("plan treatments must be removed")
	}
	if err := s.CancelPlan(3, "plan1"); code(err) != ErrInvalidState {
		t.Fatalf("double cancel must report invalid state, got %v", err)
	}

	if _, err := s.ApplyPlan(4, "plan2", "p", days, 600, 60, 0, 6*minutesPerDay); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPlan(700, "plan2"); code(err) != ErrInvalidState {
		t.Fatalf("canceling plan with started treatment must fail, got %v", err)
	}
	if _, ok := s.Treatment("plan2#0"); !ok {
		t.Fatal("rejected plan cancel must keep treatments")
	}
}

// 时钟回退：被拒操作不更新时钟；参数非法优先于时钟回退。
func TestClockAndPriority(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 10, baySpec{"G1", ZoneGeneral, false})
	regPats(t, s, 10, patSpec{"p", InfectionNegative})
	// 回退被拒。
	if _, err := s.BookTreatment(5, "t0", "p", 100, 10); code(err) != ErrClockRewind {
		t.Fatalf("expected clock rewind, got %v", err)
	}
	// 参数非法优先于时钟回退。
	if _, err := s.BookTreatment(5, "", "p", 100, 0); code(err) != ErrInvalidArgument {
		t.Fatalf("invalid argument must outrank clock rewind, got %v", err)
	}
	// 对象不存在优先于状态不符；患者不存在报 NOT_FOUND。
	if _, err := s.BookTreatment(11, "t1", "ghost", 100, 10); code(err) != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	// 被拒操作未推进时钟，now=10 仍被接受（>= last 10）。
	if _, err := s.BookTreatment(10, "t1", "p", 100, 10); err != nil {
		t.Fatalf("equal now must be accepted after rejected ops: %v", err)
	}
	// 重复治疗 id 报状态不符（对象存在性检查在参数/时钟之后）。
	if _, err := s.BookTreatment(12, "t1", "p", 500, 10); code(err) != ErrInvalidState {
		t.Fatalf("duplicate treatment id must be invalid state, got %v", err)
	}
}

// 拒绝操作不改变任何状态：故障拒绝后机位仍可用、时钟不前进。
func TestRejectedOpNoSideEffect(t *testing.T) {
	s := testSystem(t)
	regBays(t, s, 0,
		baySpec{"G1", ZoneGeneral, false},
		baySpec{"G2", ZoneGeneral, false},
	)
	regPats(t, s, 0, patSpec{"p", InfectionNegative}, patSpec{"q", InfectionNegative})
	mustBook(t, s, 1, "f1", "p", 1000, 100)
	mustBook(t, s, 2, "b1", "q", 1000, 100)
	if err := s.ReportFault(3, "G1", 500); err == nil {
		t.Fatal("expected fault rejection")
	}
	// 故障未登记：可再次在 G1 预约。
	if _, err := s.BookTreatment(3, "f2", "q", 3000, 10); err != nil {
		t.Fatalf("rejected fault must not disable bay: %v", err)
	}
}
