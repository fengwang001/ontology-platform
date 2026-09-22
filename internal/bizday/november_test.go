package bizday

import (
	"errors"
	"testing"
)

// 回归测试：11 月只有 30 天。
// 修复前 daysInMonth 把 11 月当作 31 天，导致：
//   - 11 月工作日计数多出日历上不存在的 YYYY1131；
//   - 11 月末顺延会产出 YYYY1131，下游系统拒绝；
//   - 12 月的 weekday 因 dayOfYear 累计错误整体偏移一天。

func TestDaysInMonth_AllMonths(t *testing.T) {
	// 平年各月天数表，逐月核对最后一天合法、下一天非法。
	want := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		last := 20260000 + m*100 + want[m-1]
		if err := validateDate(last); err != nil {
			t.Errorf("validateDate(%d) = %v, want nil（%d 月最后一天应合法）", last, err, m)
		}
		over := last + 1
		if err := validateDate(over); !errors.Is(err, ErrInvalidDate) {
			t.Errorf("validateDate(%d) = %v, want ErrInvalidDate（%d 月没有这一天）", over, err, m)
		}
	}
}

func TestCountBusinessDays_November(t *testing.T) {
	// 与是否闰年无关：2026/2027 为平年、2028 为闰年，11 月均为 30 天。
	cases := []struct {
		year int
		want int
	}{
		{2026, 21}, // 11/1 周日，30 天 = 4 整周 + 周日、周一
		{2027, 22}, // 11/1 周一，30 天 = 4 整周 + 周一、周二
		{2028, 22}, // 闰年，11/1 周三，30 天 = 4 整周 + 周三、周四
	}
	for _, tc := range cases {
		from := tc.year*10000 + 1101
		to := tc.year*10000 + 1201
		got, err := plainCalendar.CountBusinessDays(from, to)
		if err != nil {
			t.Fatalf("CountBusinessDays(%d, %d) err = %v", from, to, err)
		}
		if got != tc.want {
			t.Errorf("%d 年 11 月工作日 = %d, want %d", tc.year, got, tc.want)
		}
	}
}

func TestAddBusinessDays_NovemberMonthEnd(t *testing.T) {
	// 2026-11-30 是周一，顺延一个工作日必须落在 20261201（周二）；
	// 修复前会产出日历上不存在的 20261131。
	got, err := plainCalendar.AddBusinessDays(20261130, 1)
	if err != nil {
		t.Fatalf("AddBusinessDays(20261130, 1) err = %v", err)
	}
	if got != 20261201 {
		t.Errorf("AddBusinessDays(20261130, 1) = %d, want 20261201", got)
	}
}

func TestWeekday_DecemberNotShifted(t *testing.T) {
	// 11 月天数错误会经 dayOfYear 传导，使 12 月 weekday 整体偏移一天。
	// 2026-12-01 是周二（1），2026-12-06 是周日（6）。
	if got := weekday(20261201); got != 1 {
		t.Errorf("weekday(20261201) = %d, want 1（周二）", got)
	}
	if got := weekday(20261206); got != 6 {
		t.Errorf("weekday(20261206) = %d, want 6（周日）", got)
	}
	is, err := plainCalendar.IsBusinessDay(20261206)
	if err != nil {
		t.Fatalf("IsBusinessDay(20261206) err = %v", err)
	}
	if is {
		t.Error("IsBusinessDay(20261206 周日) = true, want false")
	}
}
