package bizday

import (
	"errors"
	"testing"
)

// 本文件是 11 月天数缺陷的回归测试：
// daysInMonth 曾把 11 月返回 31 天，导致 11 月工作日多算一天、
// 11 月末顺延产出不存在的 XXXX1131，且 12 月的星期几整体错一天。

// TestDaysInMonth_AllMonths 逐月核对全年 12 个月的天数（平年 + 闰年 2 月）。
func TestDaysInMonth_AllMonths(t *testing.T) {
	want := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		if got := daysInMonth(2026, m); got != want[m-1] {
			t.Errorf("daysInMonth(2026, %d) = %d, want %d", m, got, want[m-1])
		}
	}
	if got := daysInMonth(2024, 2); got != 29 {
		t.Errorf("daysInMonth(2024, 2) = %d, want 29", got)
	}
	if got := daysInMonth(2100, 2); got != 28 {
		t.Errorf("daysInMonth(2100, 2) = %d, want 28", got)
	}
}

// TestValidateDate_November31 不存在的 11 月 31 日必须被拒绝。
func TestValidateDate_November31(t *testing.T) {
	if err := validateDate(20261131); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("validateDate(20261131) = %v, want ErrInvalidDate", err)
	}
	if _, err := plainCalendar.IsBusinessDay(20261131); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("IsBusinessDay(20261131) = %v, want ErrInvalidDate", err)
	}
}

// TestShiftDay_NovemberBoundary 11 月末必须跨到 12 月 1 日，不能产出 11 月 31 日。
func TestShiftDay_NovemberBoundary(t *testing.T) {
	if got := shiftDay(20261130, 1); got != 20261201 {
		t.Errorf("shiftDay(20261130, 1) = %d, want 20261201", got)
	}
	if got := shiftDay(20261201, -1); got != 20261130 {
		t.Errorf("shiftDay(20261201, -1) = %d, want 20261130", got)
	}
}

// TestShiftDay_AllMonthEnds 逐月核对：每月最后一天 +1 必须是次月 1 日。
func TestShiftDay_AllMonthEnds(t *testing.T) {
	lastDay := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		from := 2026*10000 + m*100 + lastDay[m-1]
		nextYear, nextMonth := 2026, m+1
		if nextMonth > 12 {
			nextYear, nextMonth = 2027, 1
		}
		want := nextYear*10000 + nextMonth*100 + 1
		if got := shiftDay(from, 1); got != want {
			t.Errorf("shiftDay(%d, 1) = %d, want %d", from, got, want)
		}
	}
}

// TestCountBusinessDays_November 2026 年 11 月 1 日是周日，全月 30 天含 21 个工作日。
func TestCountBusinessDays_November(t *testing.T) {
	got, err := plainCalendar.CountBusinessDays(20261101, 20261201)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 21 {
		t.Errorf("CountBusinessDays(20261101, 20261201) = %d, want 21", got)
	}
}

// TestAddBusinessDays_NovemberMonthEnd 11 月末顺延一个工作日必须落在 12 月 1 日。
func TestAddBusinessDays_NovemberMonthEnd(t *testing.T) {
	// 2026-11-30 是周一，本身就是工作日，但 n>0 时自身不计。
	got, err := plainCalendar.AddBusinessDays(20261130, 1)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 20261201 {
		t.Errorf("AddBusinessDays(20261130, 1) = %d, want 20261201", got)
	}
	// 反向：12 月 1 日往前一个工作日是 11 月 30 日。
	got, err = plainCalendar.AddBusinessDays(20261201, -1)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 20261130 {
		t.Errorf("AddBusinessDays(20261201, -1) = %d, want 20261130", got)
	}
}

// TestWeekday_December 12 月的星期几依赖 11 月天数，回归前整体错一天。
// 2026-12-01 是周二，2026-12-31 是周四。
func TestWeekday_December(t *testing.T) {
	if got := weekday(20261201); got != 1 {
		t.Errorf("weekday(20261201) = %d, want 1 (周二)", got)
	}
	if got := weekday(20261231); got != 3 {
		t.Errorf("weekday(20261231) = %d, want 3 (周四)", got)
	}
}
