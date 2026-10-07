package imaging

import "testing"

// 测试用公共配置：
// 普通患者有效期 100，高风险 50；肾功能下限 60，上限 90；
// 水化提前量 30，预处理提前量 45；留观位 2 个，留观时长 20。
func testConfig() Config {
	return Config{
		RenalValidNormal:    100,
		RenalValidHighRisk:  50,
		RenalLower:          60,
		RenalUpper:          90,
		HydrationLead:       30,
		PremedLead:          45,
		ObservationBeds:     2,
		ObservationDuration: 20,
	}
}

func setupSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(testConfig())
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	must := func(e *Error) {
		t.Helper()
		if e != nil {
			t.Fatalf("setup: %v", e)
		}
	}
	must(s.RegisterDevice(0, "ct1", CT, 0))
	must(s.RegisterDevice(0, "ct2", CT, 0))
	must(s.RegisterDevice(0, "ct3", CT, 0))
	must(s.RegisterDevice(0, "mr3", MR, 3))
	must(s.RegisterDevice(0, "mr5", MR, 5))
	must(s.RegisterExamType(0, "ctPlain", CT, 10, false, 5))
	must(s.RegisterExamType(0, "ctEnh", CT, 10, true, 5))
	must(s.RegisterExamType(0, "mrEnh", MR, 10, true, 5))
	must(s.RegisterExamType(0, "mrPlain", MR, 10, false, 5))
	must(s.RegisterPatient(0, "pN", false, false, 0, false))
	must(s.RegisterPatient(0, "pH", true, false, 0, false))
	must(s.RegisterPatient(0, "pI", false, true, 3, false))
	must(s.RegisterPatient(0, "pA", false, false, 0, true))
	return s
}

func wantCode(t *testing.T, err *Error, code Code) {
	t.Helper()
	if code == OK {
		if err != nil {
			t.Fatalf("期望成功，实际: %v", err)
		}
		return
	}
	if err == nil || err.Code != code {
		t.Fatalf("期望 %s，实际: %v", code, err)
	}
}

// 占用区间恰相接可受理，重叠一分钟即冲突。
func TestOccupancyAdjacentAndOverlap(t *testing.T) {
	s := setupSystem(t)
	// ctPlain 占用 10 + 清洁 5，b1 占用 [100,115)。
	wantCode(t, s.Book(1, "b1", "pN", "ctPlain", "ct1", 100), OK)
	// 恰相接：[85,100) 与 [115,130) 均不重叠。
	wantCode(t, s.Book(2, "b0", "pN", "ctPlain", "ct1", 85), OK)
	wantCode(t, s.Book(3, "b2", "pN", "ctPlain", "ct1", 115), OK)
	// 差一分钟：[114,129) 与 [115,130) 重叠一分钟。
	wantCode(t, s.Book(4, "b3", "pN", "ctPlain", "ct1", 114), CodeDeviceConflict)
	// 恰相接的反向边界：[130,145) 紧邻 [115,130)。
	wantCode(t, s.Book(5, "b4", "pN", "ctPlain", "ct1", 130), OK)
}

// 质控时段按日重复：当日、次日、跨午夜的占用均须判定。
func TestQCDailyRepeat(t *testing.T) {
	s := setupSystem(t)
	// 每日 23:00-24:00 质控。
	wantCode(t, s.RegisterQC(1, "ct1", 1380, 1440), OK)
	// 当日落入质控时段：[1370,1385) 与 [1380,1440) 重叠。
	wantCode(t, s.Book(2, "q1", "pN", "ctPlain", "ct1", 1370), CodeQCConflict)
	// 跨午夜：[1435,1450) 与当日 [1380,1440) 重叠。
	wantCode(t, s.Book(3, "q2", "pN", "ctPlain", "ct1", 1435), CodeQCConflict)
	// 次日同一日内时刻重复生效：[2810,2825) 与次日 [2820,2880) 重叠。
	wantCode(t, s.Book(4, "q3", "pN", "ctPlain", "ct1", 2810), CodeQCConflict)
	// 次日质控前空闲时段可受理：[2640,2655) 不重叠。
	wantCode(t, s.Book(5, "q4", "pN", "ctPlain", "ct1", 2640), OK)
	// 跨日占用但不碰质控：[2890,2905) 位于次日质控结束之后。
	wantCode(t, s.Book(6, "q5", "pN", "ctPlain", "ct1", 2890), OK)
}

// 结果有效期恰等于仍有效，超一分钟即过期；高风险与普通患者分别配置。
func TestRenalValidityBoundary(t *testing.T) {
	s := setupSystem(t)
	// 普通患者采样时刻 10，有效期 100。
	wantCode(t, s.RegisterRenal(10, "pN", 95, 10), OK)
	// 高风险患者采样时刻 10，有效期 50。
	wantCode(t, s.RegisterRenal(10, "pH", 95, 10), OK)
	// 恰等于有效期：110-10=100，普通患者有效。
	wantCode(t, s.Book(11, "v1", "pN", "ctEnh", "ct1", 110), OK)
	// 超一分钟：111-10=101 > 100，过期。
	wantCode(t, s.Book(12, "v2", "pN", "ctEnh", "ct2", 111), CodeRenalMissingOrExpired)
	// 高风险恰等于：60-10=50，有效。
	wantCode(t, s.Book(13, "v3", "pH", "ctEnh", "ct2", 60), OK)
	// 高风险超一分钟：61-10=51 > 50，过期；同一时刻普通患者仍有效。
	wantCode(t, s.Book(14, "v4", "pH", "ctEnh", "ct3", 61), CodeRenalMissingOrExpired)
	wantCode(t, s.Book(15, "v5", "pN", "ctEnh", "ct3", 61), OK)
	// 采样时刻晚于开始时刻：视为缺失。
	wantCode(t, s.Book(16, "v6", "pN", "ctEnh", "ct1", 5), CodeRenalMissingOrExpired)
	// 无任何结果：缺失。
	wantCode(t, s.Book(17, "v7", "pA", "ctEnh", "ct1", 500), CodeRenalMissingOrExpired)
	// 非增强检查不要求肾功能结果。
	wantCode(t, s.Book(18, "v8", "pA", "ctPlain", "ct1", 500), OK)
}

// 数值恰等于下限不报肾功能不足；恰等于上限无需水化；[下限,上限) 须水化。
func TestRenalValueBoundary(t *testing.T) {
	s := setupSystem(t)
	// 低于下限：59 < 60。
	wantCode(t, s.RegisterRenal(10, "pN", 59, 10), OK)
	wantCode(t, s.Book(11, "n1", "pN", "ctEnh", "ct1", 50), CodeRenalInsufficient)
	// 恰等于下限：60，可受理但须水化。
	wantCode(t, s.RegisterRenal(12, "pN", 60, 12), OK)
	wantCode(t, s.Book(13, "n2", "pN", "ctEnh", "ct1", 50), OK)
	// 恰等于上限：90，无需水化。
	wantCode(t, s.RegisterRenal(14, "pH", 90, 14), OK)
	wantCode(t, s.Book(15, "n3", "pH", "ctEnh", "ct2", 64), OK)
	// 上限减一：89，须水化。
	wantCode(t, s.RegisterRenal(16, "pI", 89, 16), OK)
	wantCode(t, s.Book(17, "n4", "pI", "ctEnh", "ct3", 100), OK)

	// 通过签到复核观察水化要求（签到窗口 [开始-30, 开始+15]）。
	// n2（值 60，须水化）未登记水化 → 需改期，原因未水化。
	out, err := s.CheckIn(20, "n2")
	if err != nil {
		t.Fatalf("CheckIn n2: %v", err)
	}
	if out.State != StateNeedsReschedule || out.Reason != CodeNotHydrated {
		t.Fatalf("n2 期望需改期/未水化，实际 %v/%v", out.State, out.Reason)
	}
	// n3（值 90，无需水化）直接签到成功。
	out, err = s.CheckIn(34, "n3")
	if err != nil {
		t.Fatalf("CheckIn n3: %v", err)
	}
	if out.State != StateCheckedIn {
		t.Fatalf("n3 期望已签到，实际 %v", out.State)
	}
}

// 后登记覆盖先登记：只认采样时刻最晚者，并列时认登记在后者。
func TestRenalOverride(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.RegisterRenal(100, "pN", 59, 100), OK) // 采样 100，值不足
	wantCode(t, s.RegisterRenal(100, "pN", 95, 90), OK)  // 采样更早，不覆盖
	// 最新结果仍为采样 100 的 59 → 肾功能不足。
	wantCode(t, s.Book(101, "o1", "pN", "ctEnh", "ct1", 150), CodeRenalInsufficient)
	wantCode(t, s.RegisterRenal(102, "pN", 95, 100), OK) // 采样时刻并列，登记在后覆盖
	wantCode(t, s.Book(103, "o2", "pN", "ctEnh", "ct1", 150), OK)
	// 采样时刻不得晚于 now。
	wantCode(t, s.RegisterRenal(104, "pN", 95, 105), CodeInvalidParam)
}

// 水化与预处理提前量恰取等满足，晚一分钟不满足。
func TestPrepLeadBoundary(t *testing.T) {
	s := setupSystem(t)
	// pN 值 70（须水化），pA 值 95（无需水化）但有过敏史（须预处理）。
	wantCode(t, s.RegisterRenal(100, "pN", 70, 100), OK)
	wantCode(t, s.RegisterRenal(100, "pA", 95, 100), OK)
	// h1 开始 200：水化须 <= 170。
	wantCode(t, s.Book(101, "h1", "pN", "ctEnh", "ct1", 200), OK)
	wantCode(t, s.RegisterRenal(150, "pN", 70, 150), OK)
	// h2 开始 240：水化须 <= 210。
	wantCode(t, s.Book(151, "h2", "pN", "ctEnh", "ct2", 240), OK)
	// 恰取等：h1 水化 170。
	wantCode(t, s.RegisterHydration(170, "h1", 170), OK)
	out, err := s.CheckIn(171, "h1")
	if err != nil || out.State != StateCheckedIn {
		t.Fatalf("h1 期望已签到，实际 %v/%v", out.State, err)
	}
	// m1 开始 280：预处理须 <= 235。
	wantCode(t, s.RegisterRenal(180, "pA", 95, 180), OK)
	wantCode(t, s.Book(181, "m1", "pA", "ctEnh", "ct3", 280), OK)
	// 晚一分钟：h2 水化 211 > 210。
	wantCode(t, s.RegisterHydration(211, "h2", 211), OK)
	out, err = s.CheckIn(212, "h2")
	if err != nil {
		t.Fatalf("CheckIn h2: %v", err)
	}
	if out.State != StateNeedsReschedule || out.Reason != CodeNotHydrated {
		t.Fatalf("h2 期望需改期/未水化，实际 %v/%v", out.State, out.Reason)
	}
	// m2 开始 320：预处理须 <= 275。
	wantCode(t, s.RegisterRenal(230, "pA", 95, 230), OK)
	wantCode(t, s.Book(231, "m2", "pA", "mrEnh", "mr3", 320), OK)
	// 恰取等：m1 预处理 235。
	wantCode(t, s.RegisterPremed(235, "m1", 235), OK)
	out, err = s.CheckIn(250, "m1")
	if err != nil || out.State != StateCheckedIn {
		t.Fatalf("m1 期望已签到，实际 %v/%v", out.State, err)
	}
	// 晚一分钟：m2 预处理 276 > 275。
	wantCode(t, s.RegisterPremed(276, "m2", 276), OK)
	out, err = s.CheckIn(290, "m2")
	if err != nil {
		t.Fatalf("CheckIn m2: %v", err)
	}
	if out.State != StateNeedsReschedule || out.Reason != CodeNotPremedicated {
		t.Fatalf("m2 期望需改期/未预处理，实际 %v/%v", out.State, out.Reason)
	}
	// 未登记水化/预处理同样不满足。
	wantCode(t, s.RegisterRenal(291, "pN", 70, 291), OK)
	wantCode(t, s.Book(292, "h3", "pN", "ctEnh", "ct1", 320), OK)
	out, _ = s.CheckIn(293, "h3")
	if out.State != StateNeedsReschedule || out.Reason != CodeNotHydrated {
		t.Fatalf("h3 期望需改期/未水化，实际 %v/%v", out.State, out.Reason)
	}
	// 水化开始时刻不得晚于登记时的 now。
	wantCode(t, s.Book(294, "h4", "pN", "ctEnh", "ct2", 380), OK)
	wantCode(t, s.RegisterHydration(295, "h4", 300), CodeInvalidParam)
}

// 场强恰等于允许值可受理，超过则不兼容。
func TestFieldStrengthBoundary(t *testing.T) {
	s := setupSystem(t)
	// pI 允许最大场强 3；mr3 场强 3，mr5 场强 5。
	wantCode(t, s.Book(1, "f1", "pI", "mrPlain", "mr3", 100), OK)
	wantCode(t, s.Book(2, "f2", "pI", "mrPlain", "mr5", 100), CodeImplantIncompatible)
	// 无植入物患者不受场强限制。
	wantCode(t, s.Book(3, "f3", "pN", "mrPlain", "mr5", 100), OK)
	// CT 检查无场强判定。
	wantCode(t, s.Book(4, "f4", "pI", "ctPlain", "ct1", 100), OK)
}

// 留观位恰满可受理，超出一位即不足；区间恰相接不叠加。
func TestObservationCapacityBoundary(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.RegisterRenal(60, "pN", 95, 60), OK)
	wantCode(t, s.RegisterRenal(60, "pH", 95, 60), OK)
	wantCode(t, s.RegisterRenal(60, "pI", 95, 60), OK)
	// 留观区间均为 [110,130)，恰满 2 个。
	wantCode(t, s.Book(61, "c1", "pN", "ctEnh", "ct1", 100), OK)
	wantCode(t, s.Book(62, "c2", "pH", "ctEnh", "ct2", 100), OK)
	wantCode(t, s.Book(63, "c3", "pI", "ctEnh", "ct3", 100), CodeObservationFull)
	// 恰相接：[130,150) 与 [110,130) 不叠加。
	wantCode(t, s.Book(64, "c4", "pI", "ctEnh", "ct3", 120), OK)
	// 部分重叠导致超限：[115,135) 内最大人数为 2，再加一为 3 > 2。
	wantCode(t, s.Book(65, "c5", "pI", "mrEnh", "mr3", 105), CodeObservationFull)
	// 非增强检查不占留观位。
	wantCode(t, s.Book(66, "c6", "pI", "mrPlain", "mr3", 105), OK)
}

// 改约时与自身旧占用重叠不阻挡；成功后旧占用被替换。
func TestRescheduleSelfOverlap(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.Book(1, "r1", "pN", "ctPlain", "ct1", 100), OK) // 占用 [100,115)
	// 改到 105：[105,120) 与自身旧占用重叠，应成功。
	start := 105
	wantCode(t, s.Reschedule(2, "r1", &start, ""), OK)
	// 旧起点已释放：[100,105) 可被他约占用；新占用 [105,120) 阻挡他人。
	wantCode(t, s.Book(3, "r2", "pN", "ctPlain", "ct1", 90), OK)
	wantCode(t, s.Book(4, "r3", "pN", "ctPlain", "ct1", 110), CodeDeviceConflict)
	// 改约失败保持原约：目标时刻与他人冲突。
	bad := 95
	wantCode(t, s.Reschedule(5, "r1", &bad, ""), CodeDeviceConflict)
	wantCode(t, s.Book(6, "r4", "pN", "ctPlain", "ct1", 110), CodeDeviceConflict) // 原占用仍在
	// 改约到新设备：类别不符被拒，原约保持。
	wantCode(t, s.Reschedule(7, "r1", nil, "mr3"), CodeDeviceCategoryMismatch)
	wantCode(t, s.Reschedule(8, "r1", nil, "ct2"), OK)
	wantCode(t, s.Book(9, "r5", "pN", "ctPlain", "ct1", 105), OK) // ct1 上原占用已搬走
	// 改约成功后水化/预处理作废：见 TestRescheduleClearsPrep。
}

// 改约成功后已登记的水化与预处理作废，须重新登记。
func TestRescheduleClearsPrep(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.RegisterRenal(100, "pN", 70, 100), OK)
	wantCode(t, s.Book(101, "p1", "pN", "ctEnh", "ct1", 200), OK)
	// 改约成功后原水化登记作废。
	wantCode(t, s.RegisterRenal(160, "pN", 70, 160), OK)
	wantCode(t, s.RegisterHydration(170, "p1", 170), OK) // 满足 200-30
	start := 260                                         // 260-160=100 恰有效
	wantCode(t, s.Reschedule(171, "p1", &start, ""), OK)
	// 未重新登记水化 → 签到复核未水化。
	out, err := s.CheckIn(230, "p1")
	if err != nil {
		t.Fatalf("CheckIn p1: %v", err)
	}
	if out.State != StateNeedsReschedule || out.Reason != CodeNotHydrated {
		t.Fatalf("p1 期望需改期/未水化，实际 %v/%v", out.State, out.Reason)
	}
	// 需改期状态可改约：改回 260（占用已释放，重新判定）。
	wantCode(t, s.Reschedule(231, "p1", &start, ""), OK)
	// 重新登记水化后签到成功。
	wantCode(t, s.RegisterHydration(232, "p1", 230), OK)
	out, err = s.CheckIn(233, "p1")
	if err != nil || out.State != StateCheckedIn {
		t.Fatalf("p1 期望已签到，实际 %v/%v", out.State, err)
	}
}

// 签到复核失败使预约转需改期，设备占用与留观占用立即释放。
func TestCheckInFailReleasesOccupancy(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.RegisterRenal(100, "pN", 95, 100), OK)
	wantCode(t, s.Book(101, "k1", "pN", "ctEnh", "ct1", 200), OK) // 占用 [200,215)，留观 [210,230)
	// 签到前登记一个更新的不足结果，复核以签到时刻最新结果判定。
	wantCode(t, s.RegisterRenal(150, "pN", 50, 150), OK)
	out, err := s.CheckIn(170, "k1")
	if err != nil {
		t.Fatalf("CheckIn k1: %v", err)
	}
	if out.State != StateNeedsReschedule || out.Reason != CodeRenalInsufficient {
		t.Fatalf("k1 期望需改期/肾功能不足，实际 %v/%v", out.State, out.Reason)
	}
	// 设备占用已释放：同一时段可被他约占用。
	wantCode(t, s.RegisterRenal(171, "pH", 95, 171), OK)
	wantCode(t, s.Book(172, "k2", "pH", "ctEnh", "ct1", 200), OK)
	// 留观占用已释放：k2 的留观 [210,230) 不受 k1 影响（留观位 2，
	// 若未释放则 k2 与 k1 叠加仍可通过，故再补两约验证容量）。
	wantCode(t, s.RegisterRenal(173, "pI", 95, 173), OK)
	wantCode(t, s.Book(174, "k3", "pI", "ctEnh", "ct2", 200), OK)
	wantCode(t, s.Book(175, "k4", "pI", "ctEnh", "ct3", 200), CodeObservationFull)
	// 需改期状态的预约只能改约或取消。
	wantCode(t, s.CheckIn2(176, "k1"), CodeStateMismatch)
	wantCode(t, s.RegisterHydration(177, "k1", 170), CodeStateMismatch)
	wantCode(t, s.Cancel(178, "k1"), OK)
	wantCode(t, s.Cancel(179, "k1"), CodeStateMismatch)
}

// 签到窗口为 [开始-30, 开始+15]（均含）；窗口外被拒且不改变状态与时钟。
func TestCheckInWindow(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.Book(100, "w1", "pN", "ctPlain", "ct1", 200), OK)
	// 窗口 [170,215]；169 与 216 均被拒。
	wantCode(t, s.CheckIn2(169, "w1"), CodeCheckinWindow)
	wantCode(t, s.CheckIn2(216, "w1"), CodeCheckinWindow)
	// 窗口被拒不改变时钟：仍可用 now=100 接受操作。
	wantCode(t, s.Book(100, "w2", "pN", "ctPlain", "ct2", 200), OK)
	// 恰取边界：170 与 215 均可签到。
	out, err := s.CheckIn(170, "w1")
	if err != nil || out.State != StateCheckedIn {
		t.Fatalf("w1 期望已签到，实际 %v/%v", out.State, err)
	}
	out, err = s.CheckIn(215, "w2")
	if err != nil || out.State != StateCheckedIn {
		t.Fatalf("w2 期望已签到，实际 %v/%v", out.State, err)
	}
	// 已签到不可取消、不可改约、不可重复签到。
	wantCode(t, s.Cancel(216, "w1"), CodeStateMismatch)
	start := 300
	wantCode(t, s.Reschedule(217, "w1", &start, ""), CodeStateMismatch)
	wantCode(t, s.CheckIn2(218, "w1"), CodeStateMismatch)
}

// CheckIn2 是测试辅助：只关心错误码的签到调用。
func (s *System) CheckIn2(now int, id string) *Error {
	_, err := s.CheckIn(now, id)
	return err
}

// 时钟回退与被拒绝操作不改变时钟。
func TestClockRules(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.Book(100, "t1", "pN", "ctPlain", "ct1", 200), OK)
	// 回退被拒。
	wantCode(t, s.Book(99, "t2", "pN", "ctPlain", "ct1", 300), CodeClockRollback)
	// 参数非法优先于时钟回退。
	wantCode(t, s.Book(99, "", "pN", "ctPlain", "ct1", 300), CodeInvalidParam)
	// 被拒绝的操作不改变时钟：回退尝试后 now=100 仍被接受。
	wantCode(t, s.Book(100, "t2", "pN", "ctPlain", "ct1", 300), OK)
	// 其他类型的拒绝也不改变时钟。
	wantCode(t, s.Book(100, "t3", "pN", "ctPlain", "ct1", 200), CodeDeviceConflict)
	wantCode(t, s.Book(100, "t3", "pN", "ctPlain", "ct2", 200), OK)
	// 相等不重放：now 相同被接受。
	wantCode(t, s.Book(100, "t4", "pN", "ctPlain", "ct3", 200), OK)
}

// 错误优先级：同时满足多个错误时只报优先级最高者。
func TestErrorPriority(t *testing.T) {
	s := setupSystem(t)
	wantCode(t, s.RegisterQC(1, "ct1", 1380, 1440), OK)
	wantCode(t, s.Book(2, "e1", "pN", "ctPlain", "ct1", 100), OK) // 占用 [100,115)
	// 参数非法 > 时钟回退。
	wantCode(t, s.Book(0, "", "pN", "ctPlain", "ct1", 200), CodeInvalidParam)
	// 时钟回退 > 对象不存在。
	wantCode(t, s.Book(1, "e2", "nobody", "ctPlain", "ct1", 200), CodeClockRollback)
	// 对象不存在 > 设备类别不符。
	wantCode(t, s.Book(3, "e2", "nobody", "ctPlain", "mr3", 200), CodeNotFound)
	// 设备类别不符 > 植入物不兼容 > 结果缺失。
	wantCode(t, s.Book(4, "e2", "pI", "ctEnh", "mr5", 200), CodeDeviceCategoryMismatch)
	wantCode(t, s.Book(5, "e2", "pI", "mrEnh", "mr5", 200), CodeImplantIncompatible)
	// 结果缺失 > 设备时段冲突。
	wantCode(t, s.Book(6, "e2", "pN", "ctEnh", "ct1", 100), CodeRenalMissingOrExpired)
	// 肾功能不足 > 设备时段冲突。
	wantCode(t, s.RegisterRenal(7, "pN", 50, 7), OK)
	wantCode(t, s.Book(8, "e2", "pN", "ctEnh", "ct1", 100), CodeRenalInsufficient)
	// 设备时段冲突 > 质控时段冲突：e3 占用 [1365,1380)（恰止于质控开始），
	// 候选区间 [1375,1390) 既撞 e3 又撞质控 [1380,1440)。
	wantCode(t, s.RegisterRenal(1290, "pN", 95, 1290), OK)
	wantCode(t, s.Book(1291, "e3", "pN", "ctPlain", "ct1", 1365), OK)
	wantCode(t, s.Book(1292, "e2", "pN", "ctEnh", "ct1", 1375), CodeDeviceConflict)
	// 留观位不足：e4、e5 的留观 [1400,1420) 恰满，e6 在无质控的设备上超限。
	wantCode(t, s.RegisterRenal(1340, "pH", 95, 1340), OK)
	wantCode(t, s.RegisterRenal(1340, "pI", 95, 1340), OK)
	wantCode(t, s.Book(1341, "e4", "pH", "ctEnh", "ct2", 1390), OK)
	wantCode(t, s.Book(1342, "e5", "pI", "ctEnh", "ct3", 1390), OK)
	wantCode(t, s.Book(1343, "e6", "pN", "mrEnh", "mr3", 1390), CodeObservationFull)
	// 质控时段冲突 > 留观位不足：e7 既撞 ct1 质控又处留观恰满时段。
	wantCode(t, s.Book(1344, "e7", "pN", "ctEnh", "ct1", 1390), CodeQCConflict)
}
