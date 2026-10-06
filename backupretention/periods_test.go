package backupretention

import (
	"testing"
	"time"
)

// 与 Go 标准库 time（独立实现）广泛对照日/周/月周期序号。
func TestPeriodsAgainstStdlib(t *testing.T) {
	// 覆盖负时刻、边界、闰年、跨年、跨月等。
	for ts := int64(-7 * 365 * 86400); ts <= 2200_000_000; ts += 3571 {
		u := time.Unix(ts, 0).UTC()
		y, m, d := u.Date()
		midnight := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()

		if got := dayPeriod(ts); got != midnight/86400 {
			t.Fatalf("dayPeriod(%d)=%d want %d (%s)", ts, got, midnight/86400, u)
		}
		wantMonth := int64(y)*12 + int64(m) - 1
		if got := monthPeriod(ts); got != wantMonth {
			t.Fatalf("monthPeriod(%d)=%d want %d", ts, got, wantMonth)
		}
		monday := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		for monday.Weekday() != time.Monday {
			monday = monday.AddDate(0, 0, -1)
		}
		if got := weekPeriod(ts); got != monday.Unix()/86400 {
			t.Fatalf("weekPeriod(%d)=%d want %d (%s)", ts, got, monday.Unix()/86400, u)
		}
	}
}

func TestPeriodBoundaryMidnightAndOneSecond(t *testing.T) {
	// 2024-03-31 16:00 UTC == 2024-04-01 00:00 CST 之类不影响 UTC；直接用 UTC 零点。
	// 取 2024-02-29（闰日）→ 2024-03-01 边界。
	boundary := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	if dayPeriod(boundary-1) == dayPeriod(boundary) {
		t.Fatal("day period must change exactly at UTC midnight")
	}
	if dayPeriod(boundary) != dayPeriod(boundary+1) {
		t.Fatal("one second past midnight must stay in the same day")
	}
	// 月长不等：2 月结束于 29 日。
	if monthPeriod(boundary-1) == monthPeriod(boundary) {
		t.Fatal("month period must change at 2024-03-01")
	}
	// 相邻一秒月序号相同。
	if monthPeriod(boundary) != monthPeriod(boundary+1) {
		t.Fatal("one second inside a month must share period")
	}
}

func TestWeekAcrossYearAndMonth(t *testing.T) {
	// 2023-12-31 是周日，与 2024-01-01（周一）分属相邻两周。
	sun := time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC).Unix()
	mon := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if weekPeriod(sun) == weekPeriod(mon) {
		t.Fatal("week must roll over at the Sunday/Monday boundary across years")
	}
	// 同一周内周日与周一归属相同（2024-01-01..07）。
	sat := time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC).Unix()
	if weekPeriod(mon) != weekPeriod(sat) {
		t.Fatal("Mon..Sat of the same week must share the week period")
	}
}

func TestUnequalMonthLengths(t *testing.T) {
	cases := []struct {
		y, m, dim int
	}{
		{2023, 2, 28}, {2024, 2, 29}, {2024, 4, 30}, {2024, 1, 31},
	}
	for _, c := range cases {
		last := time.Date(c.y, time.Month(c.m), c.dim, 23, 59, 59, 0, time.UTC).Unix()
		next := last + 1
		if monthPeriod(last) == monthPeriod(next) {
			t.Fatalf("month should end after %d-%02d-%02d", c.y, c.m, c.dim)
		}
	}
}
