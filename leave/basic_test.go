package leave_test

import (
	"testing"

	"ontology/leave"
)

// 入职当年折算：入职当日（含）至年底剩余日数占比折算，不足一日舍去。
func TestProrationRounding(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "full", 0)
	mustRegister(t, s, 364, "lastday", 364)
	mustRegister(t, s, 365, "year1", 365) // 年 1 首日入职
	mustRegister(t, s, 830, "mid", 830)   // 年 2，doy 100，剩余 265 天

	if b := mustBalance(t, s, 0, "full"); b.CurrentGranted != 5 {
		t.Fatalf("full: granted = %d, want 5", b.CurrentGranted)
	}
	// 剩余 1 天：5*1/365 = 0（不足一日舍去）
	if b := mustBalance(t, s, 364, "lastday"); b.CurrentGranted != 0 {
		t.Fatalf("lastday: granted = %d, want 0", b.CurrentGranted)
	}
	// 剩余 265 天：5*265/365 = 3.63 -> 3
	if b := mustBalance(t, s, 830, "mid"); b.CurrentGranted != 3 {
		t.Fatalf("mid: granted = %d, want 3", b.CurrentGranted)
	}
	// 年 1 首日入职：折算 5*365/365 = 5，不受工龄档（已为 1 年档 10）影响
	if b := mustBalance(t, s, 365, "year1"); b.CurrentGranted != 5 {
		t.Fatalf("year1 hire-year: granted = %d, want 5", b.CurrentGranted)
	}
	// 次年按工龄档正常发放：工龄 1 年 -> 10
	if b := mustBalance(t, s, 730, "year1"); b.CurrentGranted != 10 {
		t.Fatalf("year1 next-year: granted = %d, want 10", b.CurrentGranted)
	}
}

// 工龄档边界恰等归高档。
func TestTenureBoundaryEquality(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	cases := []struct {
		now   int
		grant int
	}{
		{364, 5},      // 年 0，工龄 0 -> 5
		{365, 10},     // 年 1，工龄恰 1 -> 归高档 10
		{3285, 10},    // 年 9，工龄 9 -> 10
		{3650, 20},    // 年 10，工龄恰 10 -> 归高档 20
		{365 * 9, 10}, // 年 9 首日
	}
	for _, c := range cases {
		if b := mustBalance(t, s, c.now, "e"); b.CurrentGranted != c.grant {
			t.Fatalf("now=%d: granted = %d, want %d", c.now, b.CurrentGranted, c.grant)
		}
	}
}

// 结转截止日：doy 恰等截止日的计扣日可用结转额度，晚一日不可用。
func TestCarryDeadlineUsageBoundary(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	// 年 0 无使用，结转 min(5,4)=4 到年 1。
	// doy 89（恰等截止日）-> 结转；doy 90（晚一日）-> 当年。
	_, charges := mustRequest(t, s, 365, "e", 365+89, 365+90)
	expectCharges(t, charges, []leave.Charge{
		ch(365+89, 1, leave.Carried),
		ch(365+90, 1, leave.Current),
	})

	b := mustBalance(t, s, 365, "e")
	if b.CarriedGranted != 4 || b.CarriedPending != 1 || b.CurrentPending != 1 {
		t.Fatalf("balance = %+v, want carried granted 4 pending 1, current pending 1", b)
	}
}

// 跨年请假：每日按所属年度分别判定来源。
// 年度切换前提交时，次年结转尚未触达，次年计扣日只能用次年当年额度；
// 年度切换后提交的次年初请假优先用结转。
func TestCrossYearSourceAttribution(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	// now=360（年 0）：[363,367] 跨年。
	// 363,364 属年 0（doy>89，当年）；365..367 属年 1（结转未触达，当年）。
	_, charges := mustRequest(t, s, 360, "e", 363, 367)
	expectCharges(t, charges, []leave.Charge{
		ch(363, 0, leave.Current),
		ch(364, 0, leave.Current),
		ch(365, 1, leave.Current),
		ch(366, 1, leave.Current),
		ch(367, 1, leave.Current),
	})

	// 年 0 已用 2，结转 min(5-2,4)=3。
	b := mustBalance(t, s, 365, "e")
	if b.CarriedGranted != 3 || b.CurrentGranted != 10 || b.CurrentPending != 3 {
		t.Fatalf("year1 balance = %+v, want carried 3, current granted 10 pending 3", b)
	}

	// 年度切换后：doy 3,4 <= 89，优先结转。
	_, charges2 := mustRequest(t, s, 368, "e", 368, 369)
	expectCharges(t, charges2, []leave.Charge{
		ch(368, 1, leave.Carried),
		ch(369, 1, leave.Carried),
	})
	b2 := mustBalance(t, s, 368, "e")
	if b2.CarriedPending != 2 || b2.CarriedAvailable != 1 {
		t.Fatalf("year1 balance = %+v, want carried pending 2 available 1", b2)
	}
}

// 非工作日不计扣。
func TestNonWorkdaySkip(t *testing.T) {
	cfg := baseConfig()
	cfg.NonWorkdays = []int{2, 3}
	s := newService(t, cfg)
	mustRegister(t, s, 0, "e", 0)

	_, charges := mustRequest(t, s, 0, "e", 0, 4)
	expectCharges(t, charges, []leave.Charge{
		ch(0, 0, leave.Current),
		ch(1, 0, leave.Current),
		ch(4, 0, leave.Current),
	})
}
