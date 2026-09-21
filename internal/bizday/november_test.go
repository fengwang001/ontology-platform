package bizday

import (
	"errors"
	"testing"
)

// TestDaysInMonth_November 直接锁定根因：11 月只有 30 天，
// 且与闰年无关（平年 2023、闰年 2024 结果一致）。
func TestDaysInMonth_November(t *testing.T) {
	cases := []struct {
		year int
		want int
	}{
		{2023, 30}, // 平年
		{2024, 30}, // 闰年，11 月天数不应受影响
	}
	for _, tc := range cases {
		if got := daysInMonth(tc.year, 11); got != tc.want {
			t.Errorf("daysInMonth(%d, 11) = %d, want %d", tc.year, got, tc.want)
		}
	}
}

// TestNovemberPhantomDate 11 月 31 日在日历上不存在，
// 任何入口都必须以 ErrInvalidDate 拒绝它。
func TestNovemberPhantomDate(t *testing.T) {
	if _, err := plainCalendar.IsBusinessDay(20261131); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("IsBusinessDay(20261131) err = %v, want ErrInvalidDate", err)
	}
	if _, err := plainCalendar.CountBusinessDays(20261130, 20261201); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("CountBusinessDays 含 11/31 err = %v, want ErrInvalidDate", err)
	}
}

// TestNovemberMonthEndBusinessDays 锁定两个业务现象：
//   - 2026-11 整月工作日应为 21 天（11/30 是周一），修复前会被多算一天；
//   - 从 11/30（周一）顺延一个工作日应落到 12/1（周二），
//     修复前会返回不存在的 20261131（周二，恰为工作日）。
func TestNovemberMonthEndBusinessDays(t *testing.T) {
	got, err := plainCalendar.CountBusinessDays(20261101, 20261201)
	if err != nil {
		t.Fatalf("CountBusinessDays err = %v", err)
	}
	if got != 21 {
		t.Errorf("2026 年 11 月工作日 = %d, want 21", got)
	}

	got, err = plainCalendar.AddBusinessDays(20261130, 1)
	if err != nil {
		t.Fatalf("AddBusinessDays(20261130, 1) err = %v", err)
	}
	if got != 20261201 {
		t.Errorf("11 月末顺延一个工作日 = %d, want 20261201", got)
	}
}
