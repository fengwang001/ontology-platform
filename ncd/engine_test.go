package ncd

import (
	"reflect"
	"testing"
)

func testConfig() Config {
	return Config{
		MaxLevel:          5,
		RenewalGraceDays:  10,
		LiableThreshold:   50,
		ProtectStartLevel: 3,
		Premiums:          []int64{1000, 900, 800, 700, 600, 500},
	}
}

func mustEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到 %v", err)
	}
}

func mustCode(t *testing.T, err error, code Code) {
	t.Helper()
	ce, ok := err.(*Error)
	if !ok || ce.Code != code {
		t.Fatalf("期望错误[%s]，得到 %v", code, err)
	}
}

func mustLevel(t *testing.T, e *Engine, id string, want int) {
	t.Helper()
	got, err := e.Level(id)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if got != want {
		t.Fatalf("等级 = %d，期望 %d", got, want)
	}
}

func mustYears(t *testing.T, e *Engine, id string, want []YearInfo) {
	t.Helper()
	got, err := e.Years(id)
	if err != nil {
		t.Fatalf("Years: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("年度轨迹 = %+v，期望 %+v", got, want)
	}
}

func mustSurcharges(t *testing.T, e *Engine, id string, want []Surcharge) {
	t.Helper()
	got, err := e.Surcharges(id)
	if err != nil {
		t.Fatalf("Surcharges: %v", err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("追补记录 = %+v，期望 %+v", got, want)
	}
}

// 续保窗口左端与右端取等均为连续续保。
func TestRenewalWindowBoundaryInclusive(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 100)) // 年度 [100,465)，窗口 [435,475]
	if _, err := e.Renew("A", 435); err != nil {
		t.Fatalf("窗口左端取等应连续续保: %v", err)
	}
	mustYears(t, e, "A", []YearInfo{
		{Start: 100, Level: 0, Base: true},
		{Start: 465, Level: 1},
	})
	if _, err := e.Renew("A", 840); err != nil { // 第二年度窗口 [800,840]
		t.Fatalf("窗口右端取等应连续续保: %v", err)
	}
	mustLevel(t, e, "A", 2)
}

// 提前续保不提前生效：新年度自原到期日起算。
func TestEarlyRenewalNotEarlyEffective(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))            // 到期日 365
	if _, err := e.Renew("A", 335); err != nil { // 窗口左端，提前 30 天办理
		t.Fatalf("提前续保失败: %v", err)
	}
	mustYears(t, e, "A", []YearInfo{
		{Start: 0, Level: 0, Base: true},
		{Start: 365, Level: 1}, // 自原到期日起算，而非办理日 335
	})
}

// 早于窗口左端报「续保窗口外」；晚于窗口右端视为中断，等级清零。
func TestRenewalOutsideWindow(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0)) // 窗口 [335,375]
	if _, err := e.Renew("A", 334); err == nil {
		t.Fatal("窗口左端之前应拒绝")
	} else {
		mustCode(t, err, CodeOutsideRenewalWindow)
	}
	// 先积累等级：365、730 连续续保至等级 2
	if _, err := e.Renew("A", 365); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Renew("A", 730); err != nil {
		t.Fatal(err)
	}
	mustLevel(t, e, "A", 2)
	// 第三年度 [730,1095)，窗口右端 1105；1106 办理 -> 中断清零
	level, err := e.Renew("A", 1106)
	mustOK(t, err)
	if level != 0 {
		t.Fatalf("中断后等级 = %d，期望 0", level)
	}
	mustYears(t, e, "A", []YearInfo{
		{Start: 0, Level: 0, Base: true},
		{Start: 365, Level: 1},
		{Start: 730, Level: 2},
		{Start: 1106, Level: 0, Base: true}, // 中断后按首次投保处理
	})
}

// 责任比例恰等于门槛算有责，小 1 则不算。
func TestLiabilityThresholdEquality(t *testing.T) {
	build := func(ratio int) *Engine {
		e := mustEngine(t, testConfig())
		mustOK(t, e.Insure("A", "P1", 0))
		if _, err := e.Renew("A", 365); err != nil { // 等级 1
			t.Fatal(err)
		}
		if _, err := e.Renew("A", 730); err != nil { // 等级 2
			t.Fatal(err)
		}
		// 当前年度 [730,1095)，事故日落其中
		mustOK(t, e.ReportClaim("A", "C1", 800, ratio, 800))
		return e
	}
	e := build(50) // 恰等于门槛 -> 有责，等级 2-2=0
	level, err := e.Renew("A", 1095)
	mustOK(t, err)
	if level != 0 {
		t.Fatalf("责任比例恰等于门槛应有责：等级 = %d，期望 0", level)
	}
	e = build(49) // 低于门槛 -> 无责，等级 2+1=3
	level, err = e.Renew("A", 1095)
	mustOK(t, err)
	if level != 3 {
		t.Fatalf("责任比例低于门槛应无责：等级 = %d，期望 3", level)
	}
}

// 两次有责降四级；三次有责直接归零；保护不影响三次归零的次数统计。
func TestLiableClaimsDowngrade(t *testing.T) {
	cfg := testConfig()
	cfg.MaxLevel = 10
	cfg.Premiums = []int64{2000, 1900, 1800, 1700, 1600, 1500, 1400, 1300, 1200, 1100, 1000}

	// 两次有责：5 - 4 = 1
	e := mustEngine(t, cfg)
	mustOK(t, e.Insure("A", "P1", 0))
	for i := 1; i <= 5; i++ { // 连续续保至等级 5
		if _, err := e.Renew("A", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	mustOK(t, e.ReportClaim("A", "C1", 5*policyYearDays+10, 100, 5*policyYearDays+10))
	mustOK(t, e.ReportClaim("A", "C2", 5*policyYearDays+20, 60, 5*policyYearDays+20))
	level, err := e.Renew("A", 6*policyYearDays)
	mustOK(t, err)
	if level != 1 {
		t.Fatalf("两次有责应降四级：等级 = %d，期望 1", level)
	}

	// 三次有责且已购保护：仍直接归零（保护不影响次数统计）
	e = mustEngine(t, cfg)
	mustOK(t, e.Insure("A", "P1", 0))
	for i := 1; i <= 5; i++ {
		if _, err := e.Renew("A", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	mustOK(t, e.BuyProtection("A", 5*policyYearDays+1))
	for _, id := range []string{"C1", "C2", "C3"} {
		mustOK(t, e.ReportClaim("A", id, 5*policyYearDays+10, 100, 5*policyYearDays+10))
	}
	level, err = e.Renew("A", 6*policyYearDays)
	mustOK(t, err)
	if level != 0 {
		t.Fatalf("三次有责应直接归零（保护不影响次数统计）：等级 = %d", level)
	}
}

// 最高级不再升。
func TestMaxLevelCap(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	for i := 1; i <= 8; i++ { // 远超最高级 5 的次数
		if _, err := e.Renew("A", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	mustLevel(t, e, "A", 5)
}

// 保护只抵消第一次有责出险的降级：1 次有责保级，2 次有责只降 2 级。
func TestProtectionOffsetsFirstClaimOnly(t *testing.T) {
	cfg := testConfig()
	cfg.MaxLevel = 10
	cfg.Premiums = []int64{2000, 1900, 1800, 1700, 1600, 1500, 1400, 1300, 1200, 1100, 1000}
	build := func(claims int) int {
		e := mustEngine(t, cfg)
		mustOK(t, e.Insure("A", "P1", 0))
		for i := 1; i <= 5; i++ {
			if _, err := e.Renew("A", i*policyYearDays); err != nil {
				t.Fatal(err)
			}
		}
		mustOK(t, e.BuyProtection("A", 5*policyYearDays+1))
		for i := 0; i < claims; i++ {
			id := string(rune('A' + i))
			mustOK(t, e.ReportClaim("A", id, 5*policyYearDays+10, 100, 5*policyYearDays+10))
		}
		level, err := e.Renew("A", 6*policyYearDays)
		mustOK(t, err)
		return level
	}
	if got := build(1); got != 5 {
		t.Fatalf("保护下 1 次有责应保级：等级 = %d，期望 5", got)
	}
	if got := build(2); got != 3 {
		t.Fatalf("保护下 2 次有责应只降 2 级：等级 = %d，期望 3", got)
	}
}

// 保护起始级取等可购买；低一级报「等级不足」；重复购买报「已购买」。
func TestProtectionStartLevelAndDuplicate(t *testing.T) {
	e := mustEngine(t, testConfig()) // ProtectStartLevel = 3
	mustOK(t, e.Insure("A", "P1", 0))
	for i := 1; i <= 3; i++ { // 等级升到 3
		if _, err := e.Renew("A", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	mustOK(t, e.BuyProtection("A", 3*policyYearDays+1)) // 取等：允许
	mustCode(t, e.BuyProtection("A", 3*policyYearDays+2), CodeAlreadyProtected)

	e2 := mustEngine(t, testConfig())
	mustOK(t, e2.Insure("B", "P1", 0))
	for i := 1; i <= 2; i++ { // 等级 2 < 3
		if _, err := e2.Renew("B", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	mustCode(t, e2.BuyProtection("B", 2*policyYearDays+1), CodeLevelInsufficient)
}

// 迟报使出险归入历史年度，已办续保被降级并产生追补记录。
func TestLateReportLowersLevelAndSurcharges(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	if _, err := e.Renew("A", 365); err != nil { // 无出险 -> 等级 1
		t.Fatal(err)
	}
	if _, err := e.Renew("A", 730); err != nil { // 等级 2
		t.Fatal(err)
	}
	// 迟报：事故日 10 属于年度 [0,365)
	mustOK(t, e.ReportClaim("A", "L1", 10, 100, 800))
	mustYears(t, e, "A", []YearInfo{
		{Start: 0, Level: 0, Base: true, Liable: 1, Claims: 1},
		{Start: 365, Level: 0, Liable: 0}, // 1 次有责：0-2 下限 0
		{Start: 730, Level: 1},            // 重定级后 0+1
	})
	mustSurcharges(t, e, "A", []Surcharge{
		{YearIndex: 1, YearStart: 365, OldLevel: 1, NewLevel: 0, Amount: 1000 - 900},
		{YearIndex: 2, YearStart: 730, OldLevel: 2, NewLevel: 1, Amount: 900 - 800},
	})
}

// 重定级使等级升高不产生退费。
func TestRecomputeRaiseNoRefund(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	mustOK(t, e.ReportClaim("A", "C1", 10, 100, 10)) // 年度 0 内登记
	if _, err := e.Renew("A", 365); err != nil {     // 1 次有责 -> 等级 0
		t.Fatal(err)
	}
	mustLevel(t, e, "A", 0)
	mustOK(t, e.WithdrawClaim("A", "C1", 400)) // 撤销 -> 年度 1 重定级为 1
	mustLevel(t, e, "A", 1)
	mustSurcharges(t, e, "A", nil) // 升高不退费，无记录
}

// 撤销出险等价于该事故从未登记。
func TestWithdrawEquivalentToNeverRegistered(t *testing.T) {
	run := func(withClaim bool) ([]YearInfo, []Surcharge) {
		e := mustEngine(t, testConfig())
		mustOK(t, e.Insure("A", "P1", 0))
		if withClaim {
			mustOK(t, e.ReportClaim("A", "C1", 10, 100, 10))
			mustOK(t, e.WithdrawClaim("A", "C1", 20))
		}
		for i := 1; i <= 3; i++ {
			if _, err := e.Renew("A", i*policyYearDays); err != nil {
				t.Fatal(err)
			}
		}
		years, _ := e.Years("A")
		sur, _ := e.Surcharges("A")
		return years, sur
	}
	y1, s1 := run(true)
	y2, s2 := run(false)
	if !reflect.DeepEqual(y1, y2) {
		t.Fatalf("撤销后等级轨迹应与从未登记一致：%+v vs %+v", y1, y2)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("撤销后追补记录应与从未登记一致：%+v vs %+v", s1, s2)
	}
}

// 转移：原保单终止，新保单继承等级与当前年度出险记录；在保时新保报「已有在保保单」。
func TestTransferInheritsLevelAndClaims(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	if _, err := e.Renew("A", 365); err != nil { // 等级 1
		t.Fatal(err)
	}
	mustCode(t, e.Insure("A", "P2", 400), CodeActivePolicyExists) // 在保，须先转移
	mustOK(t, e.ReportClaim("A", "C1", 370, 100, 370))            // 当前年度 1 次有责
	mustOK(t, e.Transfer("A", "P2", 400))                         // 转移到新车
	mustLevel(t, e, "A", 1)                                       // 等级不变
	mustCode(t, e.Insure("A", "P3", 401), CodeActivePolicyExists) // 新保单仍在保
	pols, err := e.Policies("A")
	mustOK(t, err)
	want := []PolicyRecord{
		{PolicyID: "P1", FromDay: 0, ToDay: 400, ClosedBy: "transfer"},
		{PolicyID: "P2", FromDay: 400, ToDay: -1},
	}
	if !reflect.DeepEqual(pols, want) {
		t.Fatalf("保单审计 = %+v，期望 %+v", pols, want)
	}
	level, err := e.Renew("A", 730) // 新保单续保：继承的 1 次有责 -> 0-2 下限 0
	mustOK(t, err)
	if level != 0 {
		t.Fatalf("转移后应继承本年度出险：等级 = %d，期望 0", level)
	}
}
