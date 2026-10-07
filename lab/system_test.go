package lab

import (
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func mustErr(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，得到成功", code)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型不是 *Error: %v", err)
	}
	if e.Code != code {
		t.Fatalf("期望错误 %s，得到 %s (%v)", code, e.Code, e)
	}
}

// 常规登记：item 使用管 tubeA，时限 100 秒，不需冷藏，容忍溶血 2。
func registerDefault(t *testing.T, s *System, now int64, itemID string) {
	t.Helper()
	mustOK(t, s.RegisterItem(now, itemID, "tubeA", 100, false, 2))
}

func queryOne(t *testing.T, s *System, now int64, patientID, itemID string) ItemView {
	t.Helper()
	views, err := s.QueryPatient(now, patientID)
	mustOK(t, err)
	for _, v := range views {
		if v.ItemID == itemID {
			return v
		}
	}
	t.Fatalf("患者 %q 没有未终结项目 %q", patientID, itemID)
	return ItemView{}
}

// 送达时限：恰等于合格，晚一秒超时拒收。
func TestDeliveryDeadlineExactAndOneSecondLate(t *testing.T) {
	s := NewSystem()
	registerDefault(t, s, 0, "glu")
	mustOK(t, s.SubmitApplication(10, "app1", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(20, "tube1", "tubeA", "p1", []string{"glu"}, 20))

	// 恰等于：20 + 100 = 120，合格。
	verdicts, err := s.Sign(120, "tube1", 0)
	mustOK(t, err)
	if len(verdicts) != 1 || !verdicts[0].Accepted || verdicts[0].NewStatus != StatusQualified {
		t.Fatalf("恰到期应合格，得到 %+v", verdicts)
	}

	// 晚一秒：121 - 20 = 101 > 100，超时拒收。
	mustOK(t, s.SubmitApplication(130, "app2", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(140, "tube2", "tubeA", "p1", []string{"glu"}, 140))
	verdicts, err = s.Sign(241, "tube2", 0)
	mustOK(t, err)
	if len(verdicts) != 1 || verdicts[0].Accepted || verdicts[0].Reason != RejectTimeout {
		t.Fatalf("晚一秒应超时拒收，得到 %+v", verdicts)
	}
	if verdicts[0].NewStatus != StatusPending {
		t.Fatalf("首次拒收应回到待采集，得到 %s", verdicts[0].NewStatus)
	}
}

// 溶血等级：恰等于容忍合格，超一级拒收。
func TestHemolysisExactTolerance(t *testing.T) {
	s := NewSystem()
	registerDefault(t, s, 0, "glu") // 容忍溶血 2
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"glu"}, 2))
	verdicts, err := s.Sign(3, "tube1", 2) // 恰等于容忍
	mustOK(t, err)
	if !verdicts[0].Accepted {
		t.Fatalf("溶血恰等于容忍应合格，得到 %+v", verdicts[0])
	}

	mustOK(t, s.SubmitApplication(4, "app2", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(5, "tube2", "tubeA", "p1", []string{"glu"}, 5))
	verdicts, err = s.Sign(6, "tube2", 3) // 超一级
	mustOK(t, err)
	if verdicts[0].Accepted || verdicts[0].Reason != RejectHemolysis {
		t.Fatalf("溶血超一级应拒收，得到 %+v", verdicts[0])
	}
}

// 要求冷藏与要求常温的项目同管：常温运送只拒收要求冷藏的项目；
// 冷藏运送不影响要求常温的项目。
func TestColdChainMixedTube(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "coldItem", "tubeA", 1000, true, 4))
	mustOK(t, s.RegisterItem(0, "warmItem", "tubeA", 1000, false, 4))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"coldItem", "warmItem"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"coldItem", "warmItem"}, 2))
	mustOK(t, s.RegisterTransport(3, "tube1", false)) // 常温运送
	verdicts, err := s.Sign(4, "tube1", 0)
	mustOK(t, err)
	got := map[string]ItemVerdict{}
	for _, v := range verdicts {
		got[v.ItemID] = v
	}
	if got["coldItem"].Accepted || got["coldItem"].Reason != RejectColdChain {
		t.Fatalf("要求冷藏项目常温运送应拒收，得到 %+v", got["coldItem"])
	}
	if !got["warmItem"].Accepted {
		t.Fatalf("要求常温项目不受冷链影响应合格，得到 %+v", got["warmItem"])
	}

	// 重采 coldItem，这次冷藏运送，要求常温不影响任何项目。
	mustOK(t, s.Collect(5, "tube2", "tubeA", "p1", []string{"coldItem"}, 5))
	mustOK(t, s.RegisterTransport(6, "tube2", true))
	verdicts, err = s.Sign(7, "tube2", 0)
	mustOK(t, err)
	if !verdicts[0].Accepted {
		t.Fatalf("冷藏运送后应合格，得到 %+v", verdicts[0])
	}
}

// 同管内部分合格部分拒收，互不影响。
func TestPartialQualifyPartialReject(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "fast", "tubeA", 10, false, 4))
	mustOK(t, s.RegisterItem(0, "slow", "tubeA", 1000, false, 4))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"fast", "slow"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"fast", "slow"}, 2))
	verdicts, err := s.Sign(100, "tube1", 0) // fast 超时，slow 合格
	mustOK(t, err)
	got := map[string]ItemVerdict{}
	for _, v := range verdicts {
		got[v.ItemID] = v
	}
	if got["fast"].Accepted || got["fast"].Reason != RejectTimeout || got["fast"].NewStatus != StatusPending {
		t.Fatalf("fast 应超时拒收回到待采集，得到 %+v", got["fast"])
	}
	if !got["slow"].Accepted || got["slow"].NewStatus != StatusQualified {
		t.Fatalf("slow 应合格，得到 %+v", got["slow"])
	}
	// slow 已终结，不再出现在未终结查询中。
	views, err := s.QueryPatient(101, "p1")
	mustOK(t, err)
	if len(views) != 1 || views[0].ItemID != "fast" {
		t.Fatalf("查询应只剩 fast，得到 %+v", views)
	}
}

// 第三次拒收后项目终止，不再回到待采集；终止后可重新申请。
func TestThirdRejectionTerminates(t *testing.T) {
	s := NewSystem()
	registerDefault(t, s, 0, "glu")
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"glu"}, 7))
	now := int64(2)
	for round := 1; round <= 3; round++ {
		tubeID := string(rune('a'+round)) + "tube"
		mustOK(t, s.Collect(now, tubeID, "tubeA", "p1", []string{"glu"}, now))
		verdicts, err := s.Sign(now+1, tubeID, 4) // 溶血超限拒收
		mustOK(t, err)
		if round < 3 {
			if verdicts[0].NewStatus != StatusPending {
				t.Fatalf("第 %d 次拒收应回到待采集，得到 %s", round, verdicts[0].NewStatus)
			}
			v := queryOne(t, s, now+1, "p1", "glu")
			if v.Rejections != round || v.LastRejectReason != RejectHemolysis || v.Priority != 7 {
				t.Fatalf("第 %d 次拒收后视图不正确: %+v", round, v)
			}
		} else {
			if verdicts[0].NewStatus != StatusTerminated {
				t.Fatalf("第三次拒收应终止，得到 %s", verdicts[0].NewStatus)
			}
		}
		now += 2
	}
	// 终止后不再出现于未终结查询。
	views, err := s.QueryPatient(now, "p1")
	mustOK(t, err)
	if len(views) != 0 {
		t.Fatalf("终止后不应有未终结项目，得到 %+v", views)
	}
	// 终止的项目可以重新申请，拒收计数重新累计。
	mustOK(t, s.SubmitApplication(now, "app2", "p1", []string{"glu"}, 3))
	v := queryOne(t, s, now, "p1", "glu")
	if v.Rejections != 0 || v.Priority != 3 {
		t.Fatalf("重新申请应重置拒收计数并采用新优先级，得到 %+v", v)
	}
}

// 重采合并跨申请：两个申请的项目都被拒回待采集后可合并采集成一个新管。
func TestRecollectMergeAcrossApplications(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 1000, false, 0))
	mustOK(t, s.RegisterItem(0, "i2", "tubeA", 1000, false, 0))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"i1"}, 1))
	mustOK(t, s.SubmitApplication(2, "app2", "p1", []string{"i2"}, 2))
	// 分别采集并被拒（溶血超限，容忍 0）。
	mustOK(t, s.Collect(3, "tube1", "tubeA", "p1", []string{"i1"}, 3))
	mustOK(t, s.Collect(3, "tube2", "tubeA", "p1", []string{"i2"}, 3))
	if _, err := s.Sign(4, "tube1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sign(4, "tube2", 1); err != nil {
		t.Fatal(err)
	}
	// 跨申请合并重采。
	mustOK(t, s.Collect(5, "tube3", "tubeA", "p1", []string{"i1", "i2"}, 5))
	verdicts, err := s.Sign(6, "tube3", 0)
	mustOK(t, err)
	for _, v := range verdicts {
		if !v.Accepted {
			t.Fatalf("合并重采后应全部合格，得到 %+v", verdicts)
		}
	}
}

// 重复申请整体被拒绝且无副作用：状态、时钟均不变。
func TestDuplicateApplicationRejectedNoSideEffect(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 100, false, 2))
	mustOK(t, s.RegisterItem(0, "i2", "tubeA", 100, false, 2))
	mustOK(t, s.SubmitApplication(10, "app1", "p1", []string{"i1"}, 1))
	// i1 未终结，新申请含 i1，整体拒绝。
	mustErr(t, s.SubmitApplication(50, "app2", "p1", []string{"i1", "i2"}, 1), ErrDuplicate)
	// 无副作用：i2 未因被拒绝的申请而建立申请项。
	views, err := s.QueryPatient(10, "p1")
	mustOK(t, err)
	if len(views) != 1 || views[0].ItemID != "i1" {
		t.Fatalf("重复申请应无副作用，得到 %+v", views)
	}
	// 时钟未推进：now=20（小于被拒绝操作的 50，不小于上次被接受的 10）仍应接受。
	mustOK(t, s.SubmitApplication(20, "app3", "p1", []string{"i2"}, 1))
	// 重复的申请标识同样报重复申请。
	mustErr(t, s.SubmitApplication(30, "app1", "p2", []string{"i1"}, 1), ErrDuplicate)
}

// 取消使管作废：管内全部待签收项目取消后管作废，不能再签收。
func TestCancelVoidsTube(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 100, false, 2))
	mustOK(t, s.RegisterItem(0, "i2", "tubeA", 100, false, 2))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"i1", "i2"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"i1", "i2"}, 2))
	// 取消 i1：管内仍有 i2，管可签收。
	mustOK(t, s.Cancel(3, "p1", "i1"))
	mustOK(t, s.RegisterTransport(4, "tube1", false))
	// 取消 i2：管内再无待签收项目，管作废。
	mustOK(t, s.Cancel(5, "p1", "i2"))
	if _, err := s.Sign(6, "tube1", 0); err == nil {
		t.Fatal("作废管签收应报错")
	} else {
		mustErr(t, err, ErrStateMismatch)
	}
	mustErr(t, s.RegisterTransport(7, "tube1", true), ErrStateMismatch)
	// 已取消项目再次取消报状态不符。
	mustErr(t, s.Cancel(8, "p1", "i1"), ErrStateMismatch)
	// 已终结项目采集报状态不符。
	mustErr(t, s.Collect(9, "tube9", "tubeA", "p1", []string{"i1"}, 9), ErrStateMismatch)
}

// 取消待采集项目不影响同患者其他项目；取消已终结项目报状态不符。
func TestCancelTerminalStateMismatch(t *testing.T) {
	s := NewSystem()
	registerDefault(t, s, 0, "glu")
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"glu"}, 2))
	if _, err := s.Sign(3, "tube1", 0); err != nil {
		t.Fatal(err)
	}
	mustErr(t, s.Cancel(4, "p1", "glu"), ErrStateMismatch)
	mustErr(t, s.Cancel(4, "p1", "nosuch"), ErrNotFound)
	mustErr(t, s.Cancel(4, "nosuch", "glu"), ErrNotFound)
}

// 目录变更不追溯：已提交申请按提交时快照判定，新申请按新目录判定。
func TestCatalogChangeNotRetroactive(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "glu", "tubeA", 100, false, 2))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"glu"}, 1))
	// 变更目录：时限缩为 10，管类别改为 tubeB。
	mustOK(t, s.RegisterItem(2, "glu", "tubeB", 10, true, 0))
	// 旧申请仍按快照：tubeA、时限 100、不需冷藏、容忍 2。
	mustOK(t, s.Collect(3, "tube1", "tubeA", "p1", []string{"glu"}, 3))
	verdicts, err := s.Sign(50, "tube1", 2) // 47 秒 < 100，溶血 2 恰等于快照容忍
	mustOK(t, err)
	if !verdicts[0].Accepted {
		t.Fatalf("旧申请应按快照判定为合格，得到 %+v", verdicts[0])
	}
	// 新申请按新目录：tubeB、时限 10、需冷藏。
	mustOK(t, s.SubmitApplication(60, "app2", "p1", []string{"glu"}, 1))
	mustErr(t, s.Collect(61, "tube2", "tubeA", "p1", []string{"glu"}, 61), ErrTubeTypeMismatch)
	mustOK(t, s.Collect(61, "tube3", "tubeB", "p1", []string{"glu"}, 61))
	verdicts, err = s.Sign(80, "tube3", 0) // 19 秒 > 10，超时
	mustOK(t, err)
	if verdicts[0].Accepted || verdicts[0].Reason != RejectTimeout {
		t.Fatalf("新申请应按新目录超时拒收，得到 %+v", verdicts[0])
	}
}

// 送出登记以最后一次为准。
func TestTransportLastWins(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "coldItem", "tubeA", 1000, true, 4))
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"coldItem"}, 1))
	mustOK(t, s.Collect(2, "tube1", "tubeA", "p1", []string{"coldItem"}, 2))
	mustOK(t, s.RegisterTransport(3, "tube1", true))  // 先冷藏
	mustOK(t, s.RegisterTransport(4, "tube1", false)) // 后常温，以此为准
	verdicts, err := s.Sign(5, "tube1", 0)
	mustOK(t, err)
	if verdicts[0].Accepted || verdicts[0].Reason != RejectColdChain {
		t.Fatalf("最后一次登记为常温，应冷链拒收，得到 %+v", verdicts[0])
	}
	// 反向：先常温后冷藏，应合格。
	mustOK(t, s.Collect(6, "tube2", "tubeA", "p1", []string{"coldItem"}, 6))
	mustOK(t, s.RegisterTransport(7, "tube2", false))
	mustOK(t, s.RegisterTransport(8, "tube2", true))
	verdicts, err = s.Sign(9, "tube2", 0)
	mustOK(t, err)
	if !verdicts[0].Accepted {
		t.Fatalf("最后一次登记为冷藏，应合格，得到 %+v", verdicts[0])
	}
}

// 错误优先级：同时违反多条规则时只报优先级最高的第一个。
func TestErrorPriority(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 100, false, 2))
	mustOK(t, s.SubmitApplication(10, "app1", "p1", []string{"i1"}, 1))

	// 参数非法 > 时钟回退：空 tubeID 且 now 回退，报参数非法。
	mustErr(t, s.Collect(5, "", "tubeA", "p1", []string{"i1"}, 5), ErrInvalidParam)
	// 时钟回退 > 对象不存在：now 回退且患者不存在，报时钟回退。
	mustErr(t, s.Collect(5, "tubeX", "tubeA", "nosuch", []string{"i1"}, 5), ErrClockRollback)
	// 对象不存在 > 重复申请：项目未登记且申请标识重复，报对象不存在。
	mustErr(t, s.SubmitApplication(10, "app1", "p1", []string{"nosuch"}, 1), ErrNotFound)
	// 重复申请 > 管类别不一致：此处构造重复申请场景即可（重复优先于后续校验）。
	mustErr(t, s.SubmitApplication(10, "app2", "p1", []string{"i1"}, 1), ErrDuplicate)
	// 管类别不一致 > 状态不符：管类别错误且管标识已存在，报管类别不一致。
	mustOK(t, s.Collect(20, "tube1", "tubeA", "p1", []string{"i1"}, 20))
	mustOK(t, s.SubmitApplication(30, "app3", "p2", []string{"i1"}, 1))
	mustErr(t, s.Collect(40, "tube1", "tubeB", "p2", []string{"i1"}, 40), ErrTubeTypeMismatch)
	// 状态不符 > 时间不合理：项目非待采集且采集时刻非法，报状态不符。
	mustErr(t, s.Collect(50, "tube2", "tubeA", "p1", []string{"i1"}, 999), ErrStateMismatch)
	// 时间不合理：采集时刻晚于 now。
	mustErr(t, s.Collect(60, "tube3", "tubeA", "p2", []string{"i1"}, 61), ErrTimeUnreasonable)
	// 时间不合理：采集时刻早于项目进入待采集的时刻。
	mustErr(t, s.Collect(70, "tube4", "tubeA", "p2", []string{"i1"}, 29), ErrTimeUnreasonable)
}

// 时钟回退被拒绝且不改变状态；now 越界报参数非法。
func TestClockRules(t *testing.T) {
	s := NewSystem()
	mustErr(t, s.RegisterItem(-1, "i1", "tubeA", 100, false, 2), ErrInvalidParam)
	mustErr(t, s.RegisterItem(MaxNow+1, "i1", "tubeA", 100, false, 2), ErrInvalidParam)
	mustOK(t, s.RegisterItem(100, "i1", "tubeA", 100, false, 2))
	mustErr(t, s.RegisterItem(99, "i1", "tubeA", 100, false, 2), ErrClockRollback)
	// 被拒绝的回退操作不改变目录：i1 仍是原登记。
	mustOK(t, s.SubmitApplication(100, "app1", "p1", []string{"i1"}, 1))
	// 等于上次被接受时刻是允许的。
	mustOK(t, s.Collect(100, "tube1", "tubeA", "p1", []string{"i1"}, 100))
}

// 查询：剩余秒数（正、零、负）、拒收次数与最近拒收原因。
func TestQueryRemainingSeconds(t *testing.T) {
	s := NewSystem()
	registerDefault(t, s, 0, "glu") // 时限 100
	mustOK(t, s.SubmitApplication(1, "app1", "p1", []string{"glu"}, 1))
	mustOK(t, s.Collect(10, "tube1", "tubeA", "p1", []string{"glu"}, 10))

	remaining := func(now int64) int64 {
		t.Helper()
		v := queryOne(t, s, now, "p1", "glu")
		if v.RemainingSec == nil {
			t.Fatalf("待签收项目应有剩余秒数")
		}
		return *v.RemainingSec
	}
	if got := remaining(50); got != 60 {
		t.Fatalf("剩余应为 60，得到 %d", got)
	}
	if got := remaining(110); got != 0 {
		t.Fatalf("恰到期剩余应为 0，得到 %d", got)
	}
	if got := remaining(130); got != -20 {
		t.Fatalf("超时剩余应为 -20，得到 %d", got)
	}
	// 待采集项目无剩余秒数。
	if _, err := s.Sign(131, "tube1", 4); err != nil { // 拒回待采集
		t.Fatal(err)
	}
	v := queryOne(t, s, 132, "p1", "glu")
	if v.RemainingSec != nil {
		t.Fatalf("待采集项目不应有剩余秒数，得到 %v", *v.RemainingSec)
	}
	if v.Rejections != 1 || v.LastRejectReason != RejectTimeout {
		t.Fatalf("拒收计数或原因不正确: %+v", v)
	}
}

// 参数非法的各类情形。
func TestInvalidParams(t *testing.T) {
	s := NewSystem()
	mustErr(t, s.RegisterItem(0, "", "tubeA", 100, false, 2), ErrInvalidParam)
	mustErr(t, s.RegisterItem(0, "i1", "", 100, false, 2), ErrInvalidParam)
	mustErr(t, s.RegisterItem(0, "i1", "tubeA", 0, false, 2), ErrInvalidParam)
	mustErr(t, s.RegisterItem(0, "i1", "tubeA", -5, false, 2), ErrInvalidParam)
	mustErr(t, s.RegisterItem(0, "i1", "tubeA", 100, false, 5), ErrInvalidParam)
	mustErr(t, s.RegisterItem(0, "i1", "tubeA", 100, false, -1), ErrInvalidParam)
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 100, false, 2))

	mustErr(t, s.SubmitApplication(1, "", "p1", []string{"i1"}, 1), ErrInvalidParam)
	mustErr(t, s.SubmitApplication(1, "app1", "", []string{"i1"}, 1), ErrInvalidParam)
	mustErr(t, s.SubmitApplication(1, "app1", "p1", nil, 1), ErrInvalidParam)
	mustErr(t, s.SubmitApplication(1, "app1", "p1", []string{"i1", "i1"}, 1), ErrInvalidParam)
	eleven := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	mustErr(t, s.SubmitApplication(1, "app1", "p1", eleven, 1), ErrInvalidParam)

	_, err := s.Sign(1, "", 0)
	mustErr(t, err, ErrInvalidParam)
	_, err = s.Sign(1, "tube1", 5)
	mustErr(t, err, ErrInvalidParam)
	_, err = s.Sign(1, "tube1", -1)
	mustErr(t, err, ErrInvalidParam)
	mustErr(t, s.RegisterTransport(1, "", true), ErrInvalidParam)
	mustErr(t, s.Cancel(1, "", "i1"), ErrInvalidParam)
	_, err = s.QueryPatient(1, "")
	mustErr(t, err, ErrInvalidParam)
}

// 并发调用：结果等价于某个串行顺序，且不变量成立
// （拒收计数不超过 3、终止项目不会回到待采集）。
func TestConcurrentLinearizable(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterItem(0, "i1", "tubeA", 50, true, 1))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			patientID := "p" + string(rune('a'+g))
			now := int64(0)
			appSeq := 0
			for round := 0; round < 50; round++ {
				now += int64(g + 1)
				appSeq++
				appID := patientID + "-app" + itoa(appSeq)
				if err := s.SubmitApplication(now, appID, patientID, []string{"i1"}, g); err != nil {
					continue // 重复申请等，跳过
				}
				tubeID := appID + "-tube"
				if err := s.Collect(now, tubeID, "tubeA", patientID, []string{"i1"}, now); err != nil {
					continue
				}
				_ = s.RegisterTransport(now, tubeID, round%2 == 0)
				_, _ = s.Sign(now+int64(round%120), tubeID, round%5)
				_, _ = s.QueryPatient(now+200, patientID)
			}
		}(g)
	}
	wg.Wait()
	// 不变量检查：所有未终结项目的拒收计数不超过 3。
	for g := 0; g < 8; g++ {
		patientID := "p" + string(rune('a'+g))
		views, err := s.QueryPatient(MaxNow, patientID)
		if err != nil {
			continue
		}
		for _, v := range views {
			if v.Rejections > MaxRejections {
				t.Fatalf("患者 %s 项目 %s 拒收计数 %d 超过上限", patientID, v.ItemID, v.Rejections)
			}
			if v.Status.Terminal() {
				t.Fatalf("终结项目不应出现在未终结查询: %+v", v)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
