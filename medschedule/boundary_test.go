package medschedule

import (
	"errors"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func errCodeOf(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

func assertCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if errCodeOf(err) != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

// 窗口边界：恰取等（p-W、p+W）合法；差一秒无对应计划点。
func TestWindowBoundaries(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 5))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 100, H: 100}))
	assertCode(t, s.Administer(89, "O"), ErrNoScheduledPoint)
	must(t, s.Administer(90, "O"))
	must(t, s.Administer(210, "O"))
	assertCode(t, s.Administer(211, "O"), ErrNoScheduledPoint)
}

// 漏给边界：now == p+W 仍待给；now == p+W+1 为漏给。
func TestMissedBoundary(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 5))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 100, H: 1000}))
	got, _ := s.QueryPatient(110, "P", 100, 100)
	if got[0].Status != StatusPending {
		t.Fatalf("at p+W should be pending, got %s", got[0].Status)
	}
	got, _ = s.QueryPatient(111, "P", 100, 100)
	if got[0].Status != StatusMissed {
		t.Fatalf("at p+W+1 should be missed, got %s", got[0].Status)
	}
}

// 补给两个限制恰取等：下一点窗口起始、同药品安全间隔。
func TestMakeUpBoundaries(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 1))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 100}))
	// 恰等于下一点窗口起始时已无可补给点，按优先级报无对应计划点。
	assertCode(t, s.MakeUp(90, "O"), ErrNoScheduledPoint)
	must(t, s.MakeUp(89, "O"))
	got, _ := s.QueryPatient(89, "P", 0, 400)
	statusAt := map[int64]Status{}
	for _, p := range got {
		statusAt[p.Time] = p.Status
	}
	if statusAt[100] != StatusVoid {
		t.Fatalf("old-grid 100 should be void, got %s", statusAt[100])
	}
	if statusAt[189] != StatusPending {
		t.Fatalf("new grid 189 should be pending, got %s", statusAt[189])
	}

	s2, _ := NewSystem(10)
	must(t, s2.RegisterDrug(0, "D2", "C", 30))
	// O1 点 0 按时给药；O2 首点 20，其漏给可补给区间 (30,110)。
	must(t, s2.CreateOrder(0, Spec{ID: "O1", Patient: "P", Drug: "D2", Kind: "interval", FirstTime: 0, H: 100}))
	must(t, s2.CreateOrder(0, Spec{ID: "O2", Patient: "P", Drug: "D2", Kind: "interval", FirstTime: 10, H: 100}))
	must(t, s2.Administer(0, "O1"))
	assertCode(t, s2.MakeUp(29, "O2"), ErrIntervalTooShort) // 间隔 29<30 不足
	must(t, s2.MakeUp(30, "O2"))                            // 恰 30 允许
}

// 固定时点补给后后续点不变。
func TestDailyMakeUpNoReschedule(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 1))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "daily", TimesOfDay: []int64{0, 43200}}))
	must(t, s.MakeUp(43189, "O"))
	got, _ := s.QueryPatient(43189, "P", 0, 86400)
	statusAt := map[int64]Status{}
	for _, p := range got {
		statusAt[p.Time] = p.Status
	}
	if statusAt[0] != StatusMadeUp || statusAt[43200] != StatusPending || statusAt[86400] != StatusPending {
		t.Fatalf("daily makeup must not reschedule: %+v", statusAt)
	}
}

// 停嘱时漏给与作废的分界。
func TestStopBoundary(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 1))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 1000}))
	// 点 p=0，p+W=10。停嘱于 11：10 < 11 => 保持漏给。
	must(t, s.StopOrder(11, "O"))
	got, _ := s.QueryPatient(11, "P", 0, 1000)
	if got[0].Status != StatusMissed {
		t.Fatalf("p+W < stop should be missed, got %s", got[0].Status)
	}

	s2, _ := NewSystem(10)
	must(t, s2.RegisterDrug(0, "D", "C", 1))
	must(t, s2.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 1000}))
	// 停嘱于 10：p+W=10 恰等于停嘱时刻，不满足严格小于 => 作废。
	must(t, s2.StopOrder(10, "O"))
	got2, _ := s2.QueryPatient(10, "P", 0, 1000)
	if got2[0].Status != StatusVoid {
		t.Fatalf("p+W == stop should be void, got %s", got2[0].Status)
	}
	assertCode(t, s2.Administer(10, "O"), ErrBadState)
	assertCode(t, s2.MakeUp(10, "O"), ErrBadState)
}

// 改嘱原子性与时钟不推进。
func TestReplaceAtomicity(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 5))
	must(t, s.CreateOrder(0, Spec{ID: "OLD", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 100}))
	assertCode(t, s.ReplaceOrder(50, "OLD", Spec{ID: "NEW", Patient: "P", Drug: "NOPE", Kind: "interval", FirstTime: 50, H: 100}), ErrNotFound)
	got, _ := s.QueryPatient(50, "P", 0, 1000)
	for _, p := range got {
		if p.Status == StatusVoid {
			t.Fatalf("old order must survive failed replace: %+v", got)
		}
	}
	// 拒绝的改嘱不推进时钟：先让时钟成功走到 50，再证明失败的改嘱不改变状态。
	// 用独立一次成功改嘱后的回退尝试来验证时钟推进语义。
	must(t, s.SetAllergy(50, "Z", "whatever", true))
	err := s.StopOrder(49, "OLD")
	assertCode(t, err, ErrClockRollback)
	must(t, s.ReplaceOrder(50, "OLD", Spec{ID: "NEW", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 50, H: 100}))
	must(t, s.Administer(50, "NEW"))
	assertCode(t, s.Administer(50, "OLD"), ErrBadState)
}

// 跨医嘱同药品最小安全间隔。
func TestCrossOrderSafetyGap(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 40))
	must(t, s.CreateOrder(0, Spec{ID: "A", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 100}))
	must(t, s.CreateOrder(0, Spec{ID: "B", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 20, H: 100}))
	must(t, s.Administer(0, "A"))
	assertCode(t, s.MakeUp(39, "B"), ErrIntervalTooShort)
	must(t, s.MakeUp(40, "B")) // 40-0=40 恰等于安全间隔；40 < 120-10
	must(t, s.CreateOrder(40, Spec{ID: "C", Patient: "Q", Drug: "D", Kind: "interval", FirstTime: 40, H: 100}))
	must(t, s.Administer(40, "C")) // 不同患者，不受 P 的给药影响
}

// PRN 滚动窗口取等与同医嘱间隔取等。
func TestPRNRollingWindow(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 1))
	must(t, s.CreateOrder(0, Spec{ID: "R", Patient: "P", Drug: "D", Kind: "prn", PRNMinGap: 1, PRNMax24: 2}))
	must(t, s.AdministerPRN(0, "R"))
	must(t, s.AdministerPRN(100, "R"))
	assertCode(t, s.AdministerPRN(200, "R"), ErrPRNLimit)
	must(t, s.AdministerPRN(86400, "R"))

	s2, _ := NewSystem(10)
	must(t, s2.RegisterDrug(0, "E", "C", 1))
	must(t, s2.CreateOrder(0, Spec{ID: "R", Patient: "P", Drug: "E", Kind: "prn", PRNMinGap: 60, PRNMax24: 10}))
	must(t, s2.AdministerPRN(0, "R"))
	assertCode(t, s2.AdministerPRN(59, "R"), ErrIntervalTooShort)
	must(t, s2.AdministerPRN(60, "R"))
}

// 过敏命中药品与类别；变更不追溯已开医嘱。
func TestAllergyDrugAndCategory(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D1", "CAT1", 5))
	must(t, s.RegisterDrug(0, "D2", "CAT2", 5))
	must(t, s.SetAllergy(0, "P", "D1", true))
	assertCode(t, s.CreateOrder(0, Spec{ID: "O1", Patient: "P", Drug: "D1", Kind: "interval", FirstTime: 0, H: 100}), ErrAllergy)
	must(t, s.SetAllergy(0, "P", "CAT2", true))
	assertCode(t, s.CreateOrder(0, Spec{ID: "O2", Patient: "P", Drug: "D2", Kind: "interval", FirstTime: 0, H: 100}), ErrAllergy)
	must(t, s.CreateOrder(0, Spec{ID: "O3", Patient: "Q", Drug: "D2", Kind: "interval", FirstTime: 0, H: 100}))
	must(t, s.SetAllergy(0, "Q", "CAT2", true))
	must(t, s.Administer(0, "O3"))
	must(t, s.SetAllergy(5, "P", "D1", false))
	must(t, s.CreateOrder(5, Spec{ID: "O4", Patient: "P", Drug: "D1", Kind: "interval", FirstTime: 5, H: 100}))
}

// 错误优先级与频次参数非法。
func TestErrorPriority(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(100, "D", "C", 5))
	assertCode(t, s.StopOrder(-1, ""), ErrInvalidParam)
	assertCode(t, s.StopOrder(50, "MISSING"), ErrClockRollback)
	assertCode(t, s.StopOrder(100, "MISSING"), ErrNotFound)
	must(t, s.CreateOrder(100, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 100, H: 100}))
	must(t, s.StopOrder(200, "O"))
	assertCode(t, s.StopOrder(200, "O"), ErrBadState)
	must(t, s.SetAllergy(200, "P", "D", true))
	assertCode(t, s.CreateOrder(200, Spec{ID: "O2", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 200, H: 100}), ErrAllergy)
	assertCode(t, s.CreateOrder(200, Spec{ID: "O3", Patient: "Q", Drug: "D", Kind: "interval", FirstTime: 200, H: 20}), ErrInvalidParam)
	s2, _ := NewSystem(100)
	must(t, s2.RegisterDrug(0, "X", "C", 500))
	assertCode(t, s2.CreateOrder(0, Spec{ID: "X", Patient: "Q", Drug: "X", Kind: "interval", FirstTime: 0, H: 300}), ErrInvalidParam)
}

// 拒服不计实际给药，不影响间隔判定。
func TestRefuseDoesNotAffectGap(t *testing.T) {
	s, _ := NewSystem(10)
	must(t, s.RegisterDrug(0, "D", "C", 1000))
	must(t, s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D", Kind: "interval", FirstTime: 0, H: 1000}))
	must(t, s.Refuse(0, "O"))
	must(t, s.Refuse(1000, "O"))
	got, _ := s.QueryPatient(1000, "P", 0, 1000)
	if got[0].Status != StatusRefused {
		t.Fatalf("want refused, got %s", got[0].Status)
	}
	// 若拒服被误记为给药，下面的给药会因 1000 秒安全间隔失败。
	must(t, s.Administer(2000, "O"))
}
