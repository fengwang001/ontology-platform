package ncd

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// 拒绝次序逐对验证：同一操作同时满足两个拒绝条件时，只报次序最前者。
func TestRejectionOrderPairs(t *testing.T) {
	newEngine := func(t *testing.T) *Engine {
		e := mustEngine(t, testConfig())
		mustOK(t, e.Insure("A", "P1", 100)) // 年度 [100,465)，时钟 100
		return e
	}
	t.Run("参数非法>被保人不存在", func(t *testing.T) {
		e := newEngine(t)
		mustCode(t, e.ReportClaim("B", "C1", 10, 200, 100), CodeInvalidParam)
	})
	t.Run("被保人不存在>时钟回退", func(t *testing.T) {
		e := newEngine(t)
		mustCode(t, e.ReportClaim("B", "C1", 10, 50, 50), CodeInsuredNotFound)
	})
	t.Run("时钟回退>已有在保保单", func(t *testing.T) {
		e := newEngine(t)
		mustCode(t, e.Insure("A", "P2", 50), CodeClockRollback)
	})
	t.Run("时钟回退>事故已存在", func(t *testing.T) {
		e := newEngine(t)
		mustOK(t, e.ReportClaim("A", "C1", 110, 50, 110))
		mustCode(t, e.ReportClaim("A", "C1", 100, 50, 105), CodeClockRollback)
	})
	t.Run("事故已存在>事故日未承保", func(t *testing.T) {
		e := newEngine(t)
		mustOK(t, e.ReportClaim("A", "C1", 110, 50, 110))
		mustCode(t, e.ReportClaim("A", "C1", 99999, 50, 100000), CodeClaimExists)
	})
	t.Run("时钟回退>事故不存在", func(t *testing.T) {
		e := newEngine(t)
		mustCode(t, e.WithdrawClaim("A", "C9", 50), CodeClockRollback)
	})
	t.Run("等级不足>已购买", func(t *testing.T) {
		e := mustEngine(t, testConfig())
		mustOK(t, e.Insure("A", "P1", 0))
		for i := 1; i <= 3; i++ { // 等级 3，购买保护
			if _, err := e.Renew("A", i*policyYearDays); err != nil {
				t.Fatal(err)
			}
		}
		mustOK(t, e.BuyProtection("A", 3*policyYearDays+1))
		// 迟报 2 次有责到上一年度 -> 当前年度被重定级为 0，等级不足且已购买同时成立
		mustOK(t, e.ReportClaim("A", "L1", 2*policyYearDays+10, 100, 3*policyYearDays+2))
		mustOK(t, e.ReportClaim("A", "L2", 2*policyYearDays+20, 100, 3*policyYearDays+3))
		mustLevel(t, e, "A", 0)
		mustCode(t, e.BuyProtection("A", 3*policyYearDays+4), CodeLevelInsufficient)
	})
	t.Run("时钟回退>续保窗口外", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.Renew("A", 50) // 既回退又窗口外
		mustCode(t, err, CodeClockRollback)
	})
	t.Run("被保人不存在>续保窗口外", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.Renew("B", 100)
		mustCode(t, err, CodeInsuredNotFound)
	})
}

// 被拒操作不得改变等级、出险记录、保护状态与当前时刻。
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	if _, err := e.Renew("A", 365); err != nil {
		t.Fatal(err)
	}
	mustOK(t, e.ReportClaim("A", "C1", 370, 100, 370))
	snap := func() (int, []YearInfo, []Surcharge) {
		years, _ := e.Years("A")
		sur, _ := e.Surcharges("A")
		return e.Now(), years, sur
	}
	now0, years0, sur0 := snap()
	rejected := []func() error{
		func() error { return e.Insure("", "P9", 400) },
		func() error { return e.Insure("A", "P9", 399) },                 // 时钟回退
		func() error { return e.Insure("A", "P9", 400) },                 // 已有在保保单
		func() error { return e.ReportClaim("A", "C1", 371, 60, 400) },   // 事故已存在
		func() error { return e.WithdrawClaim("A", "C9", 400) },          // 事故不存在
		func() error { return e.ReportClaim("A", "C2", 99999, 60, 400) }, // 事故日未承保
		func() error { return e.BuyProtection("B", 400) },                // 被保人不存在
		func() error { return e.BuyProtection("A", 400) },                // 等级不足
		func() error { _, err := e.Renew("A", 699); return err },         // 续保窗口外
		func() error { return e.Transfer("B", "P9", 400) },               // 被保人不存在
	}
	for i, op := range rejected {
		if err := op(); err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
		now, years, sur := snap()
		if now != now0 || !reflect.DeepEqual(years, years0) || !reflect.DeepEqual(sur, sur0) {
			t.Fatalf("第 %d 个被拒操作留下痕迹：now %d->%d", i, now0, now)
		}
	}
}

// 常规续保定级开销不随历史年度数增长：用年度记录访问计数证明。
func TestRenewalCostIndependentOfHistory(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	stepsAt := func(n int) int {
		for len := 0; ; {
			_ = len
			break
		}
		_, err := e.Renew("A", n*policyYearDays)
		mustOK(t, err)
		return e.LastOpSteps()
	}
	var s10, s2000 int
	for i := 1; i <= 2000; i++ {
		s := stepsAt(i)
		if i == 10 {
			s10 = s
		}
		if i == 2000 {
			s2000 = s
		}
	}
	t.Logf("10 年历史续保访问年度数=%d，2000 年历史=%d", s10, s2000)
	if s10 != s2000 || s2000 != 1 {
		t.Fatalf("续保定级应只访问 1 条年度记录：10 年=%d，2000 年=%d", s10, s2000)
	}
}

// 迟报重定级范围限于事故日所在年度及之后。
func TestRecomputeScopeLimited(t *testing.T) {
	e := mustEngine(t, testConfig())
	mustOK(t, e.Insure("A", "P1", 0))
	for i := 1; i <= 100; i++ {
		if _, err := e.Renew("A", i*policyYearDays); err != nil {
			t.Fatal(err)
		}
	}
	// 事故日落在第 40 个年度（下标 40，共 101 个年度）：应访问 101-1-40=60 条
	mustOK(t, e.ReportClaim("A", "L1", 40*policyYearDays+1, 100, 101*policyYearDays))
	if got, want := e.LastOpSteps(), 60; got != want {
		t.Fatalf("重定级访问年度数 = %d，期望 %d", got, want)
	}
}

// 相同操作序列重放得到完全相同的等级轨迹与追补记录。
func TestReplayDeterminism(t *testing.T) {
	script := func(e *Engine) {
		mustOK(t, e.Insure("A", "P1", 0))
		for i := 1; i <= 3; i++ {
			if _, err := e.Renew("A", i*policyYearDays); err != nil {
				t.Fatal(err)
			}
		}
		mustOK(t, e.BuyProtection("A", 3*policyYearDays+1))
		mustOK(t, e.ReportClaim("A", "L1", 10, 80, 3*policyYearDays+2))
		mustOK(t, e.Transfer("A", "P2", 3*policyYearDays+3))
		if _, err := e.Renew("A", 4*policyYearDays); err != nil {
			t.Fatal(err)
		}
		mustOK(t, e.WithdrawClaim("A", "L1", 4*policyYearDays+1))
	}
	trace := func() ([]YearInfo, []Surcharge) {
		e := mustEngine(t, testConfig())
		script(e)
		years, _ := e.Years("A")
		sur, _ := e.Surcharges("A")
		return years, sur
	}
	y1, s1 := trace()
	y2, s2 := trace()
	if !reflect.DeepEqual(y1, y2) || !reflect.DeepEqual(s1, s2) {
		t.Fatalf("重放结果不一致：%+v/%+v vs %+v/%+v", y1, s1, y2, s2)
	}
}

// 同一被保人的迟报与续保并发到达：结果等价于某种串行顺序。
func TestConcurrentLateReportAndRenew(t *testing.T) {
	for trial := 0; trial < 200; trial++ {
		e := mustEngine(t, testConfig())
		mustOK(t, e.Insure("A", "P1", 0))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = e.ReportClaim("A", "L1", 10, 100, 365) }()
		go func() { defer wg.Done(); _, _ = e.Renew("A", 365) }()
		wg.Wait()
		// 串行顺序一：先登记后续保 -> 等级 0，无追补
		// 串行顺序二：先续保后迟报 -> 等级先 1 后重定级为 0，产生 1 条追补
		mustLevel(t, e, "A", 0)
		sur, _ := e.Surcharges("A")
		switch len(sur) {
		case 0:
		case 1:
			want := Surcharge{YearIndex: 1, YearStart: 365, OldLevel: 1, NewLevel: 0, Amount: 100}
			if sur[0] != want {
				t.Fatalf("追补记录 = %+v，期望 %+v", sur[0], want)
			}
		default:
			t.Fatalf("追补记录条数 = %d，期望 0 或 1", len(sur))
		}
	}
}

// 多被保人并发混合操作：与逐被保人串行执行结果一致（配合 -race）。
func TestConcurrentDisjointInsureds(t *testing.T) {
	const insureds = 16
	id := func(i int) string { return fmt.Sprintf("I%d", i) }
	// 全局时钟只能前进，故按步推进：同一步内各被保人用相同时刻并发执行。
	step := func(e *Engine, i, s int) {
		day := s * policyYearDays
		switch {
		case s == 0:
			mustOK(t, e.Insure(id(i), "P-"+id(i), day))
		case s == 6:
			mustOK(t, e.ReportClaim(id(i), "L1", 10, 100, day))
		default:
			if _, err := e.Renew(id(i), day); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 串行基准
	base := mustEngine(t, testConfig())
	for s := 0; s <= 7; s++ {
		for i := 0; i < insureds; i++ {
			step(base, i, s)
		}
	}
	// 并发执行：同一步内并发，步间以 WaitGroup 同步
	e := mustEngine(t, testConfig())
	for s := 0; s <= 7; s++ {
		var wg sync.WaitGroup
		for i := 0; i < insureds; i++ {
			wg.Add(1)
			go func(i, s int) { defer wg.Done(); step(e, i, s) }(i, s)
		}
		wg.Wait()
	}
	for i := 0; i < insureds; i++ {
		y1, _ := base.Years(id(i))
		y2, _ := e.Years(id(i))
		s1, _ := base.Surcharges(id(i))
		s2, _ := e.Surcharges(id(i))
		if !reflect.DeepEqual(y1, y2) || !reflect.DeepEqual(s1, s2) {
			t.Fatalf("被保人 %s 并发结果与串行不一致", id(i))
		}
	}
}
