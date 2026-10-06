package backup

import (
	"testing"
	"time"
)

func ts(year int, month time.Month, day, hour, min, sec int) int64 {
	return time.Date(year, month, day, hour, min, sec, 0, time.UTC).Unix()
}

func TestDayIndexBoundary(t *testing.T) {
	cases := []struct {
		t    int64
		want int64
	}{
		{0, 0},
		{1, 0},
		{secondsPerDay - 1, 0},
		{secondsPerDay, 1},
		{secondsPerDay + 1, 1},
		{2*secondsPerDay - 1, 1},
		{2 * secondsPerDay, 2},
	}
	for _, c := range cases {
		if got := dayIndex(c.t); got != c.want {
			t.Errorf("dayIndex(%d) = %d, want %d", c.t, got, c.want)
		}
	}
}

func TestWeekIndexBoundary(t *testing.T) {
	// 1970-01-01 是周四；第一个周一是 1970-01-05（第 4 天）。
	monday := int64(4) * secondsPerDay
	cases := []struct {
		t    int64
		want int64
	}{
		{0, 0},
		{monday - 1, 0}, // 周日最后一秒仍属上一周
		{monday, 1},     // 周一零点整进入新一周
		{monday + 1, 1},
		{monday + 7*secondsPerDay - 1, 1},
		{monday + 7*secondsPerDay, 2},
	}
	for _, c := range cases {
		if got := weekIndex(c.t); got != c.want {
			t.Errorf("weekIndex(%d) = %d, want %d", c.t, got, c.want)
		}
	}
}

func TestWeekIndexAcrossYearAndMonth(t *testing.T) {
	// 2020-12-28（周一）到 2021-01-03（周日）是同一周，跨年跨月不另起周。
	base := weekIndex(ts(2020, time.December, 28, 0, 0, 0))
	for d := 29; d <= 31; d++ {
		if got := weekIndex(ts(2020, time.December, d, 12, 0, 0)); got != base {
			t.Errorf("2020-12-%d 周序号 = %d, want %d", d, got, base)
		}
	}
	for d := 1; d <= 3; d++ {
		if got := weekIndex(ts(2021, time.January, d, 12, 0, 0)); got != base {
			t.Errorf("2021-01-%d 周序号 = %d, want %d", d, got, base)
		}
	}
	// 2021-01-04（周一）零点整进入新一周。
	if got := weekIndex(ts(2021, time.January, 4, 0, 0, 0)); got != base+1 {
		t.Errorf("2021-01-04 周序号 = %d, want %d", got, base+1)
	}
	if got := weekIndex(ts(2021, time.January, 3, 23, 59, 59)); got != base {
		t.Errorf("2021-01-03 23:59:59 周序号 = %d, want %d", got, base)
	}
}

func TestWeekIndexConsistentWithMondays(t *testing.T) {
	// 与 time 包对照：跨越约 6 年逐日检查，周序号恰在周一零点递增。
	start := ts(2019, time.January, 1, 0, 0, 0)
	end := ts(2025, time.January, 1, 0, 0, 0)
	prev := weekIndex(start - 1)
	for cur := start; cur < end; cur += secondsPerDay {
		got := weekIndex(cur)
		wd := time.Unix(cur, 0).UTC().Weekday()
		if wd == time.Monday {
			if got != prev+1 {
				t.Fatalf("t=%d 周一：周序号 %d, want %d", cur, got, prev+1)
			}
		} else if got != prev {
			t.Fatalf("t=%d 非周一：周序号 %d, want %d", cur, got, prev)
		}
		prev = got
	}
}

func TestMonthIndexUnequalLengths(t *testing.T) {
	jan := monthIndex(ts(2021, time.January, 15, 0, 0, 0))
	// 1 月 31 日最后一秒与 2 月 1 日零点整分属相邻两月。
	if got := monthIndex(ts(2021, time.January, 31, 23, 59, 59)); got != jan {
		t.Errorf("1 月末月序号 = %d, want %d", got, jan)
	}
	if got := monthIndex(ts(2021, time.February, 1, 0, 0, 0)); got != jan+1 {
		t.Errorf("2 月初月序号 = %d, want %d", got, jan+1)
	}
	// 平年 2 月 28 天。
	feb := monthIndex(ts(2021, time.February, 28, 23, 59, 59))
	if feb != jan+1 {
		t.Errorf("平年 2 月末月序号 = %d, want %d", feb, jan+1)
	}
	if got := monthIndex(ts(2021, time.March, 1, 0, 0, 0)); got != jan+2 {
		t.Errorf("3 月初月序号 = %d, want %d", got, jan+2)
	}
	// 闰年 2 月 29 天。
	leapFeb := monthIndex(ts(2020, time.February, 29, 12, 0, 0))
	if got := monthIndex(ts(2020, time.March, 1, 0, 0, 0)); got != leapFeb+1 {
		t.Errorf("闰年 3 月初月序号 = %d, want %d", got, leapFeb+1)
	}
	// 跨年：12 月与次年 1 月相邻。
	dec := monthIndex(ts(2021, time.December, 31, 23, 59, 59))
	if got := monthIndex(ts(2022, time.January, 1, 0, 0, 0)); got != dec+1 {
		t.Errorf("跨年 1 月初月序号 = %d, want %d", got, dec+1)
	}
}
