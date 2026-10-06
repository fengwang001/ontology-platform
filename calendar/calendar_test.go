package calendar

import "testing"

func TestRoundTrip(t *testing.T) {
	for z := -1000000; z <= 1000000; z++ {
		y, m, d := ToCivil(z)
		if got := FromCivil(y, m, d); got != z {
			t.Fatalf("round trip failed: z=%d -> %d-%d-%d -> %d", z, y, m, d, got)
		}
	}
}

func TestKnownDates(t *testing.T) {
	cases := []struct {
		y, m, d, z int
	}{
		{1970, 1, 1, 0},
		{1970, 1, 2, 1},
		{1969, 12, 31, -1},
		{2000, 2, 29, 11016},
		{2024, 2, 29, 19782},
		{1900, 3, 1, -25508}, // 1900 非闰年
	}
	for _, c := range cases {
		if got := FromCivil(c.y, c.m, c.d); got != c.z {
			t.Errorf("FromCivil(%d,%d,%d)=%d want %d", c.y, c.m, c.d, got, c.z)
		}
		y, m, d := ToCivil(c.z)
		if y != c.y || m != c.m || d != c.d {
			t.Errorf("ToCivil(%d)=%d-%d-%d want %d-%d-%d", c.z, y, m, d, c.y, c.m, c.d)
		}
	}
}

func ymd(a, b, c int) [3]int { return [3]int{a, b, c} }

func TestAddMonths(t *testing.T) {
	cases := []struct {
		start [3]int
		n     int
		want  [3]int
	}{
		// 月末日期推算：目标月无对应日号取该月最后一天
		{ymd(2024, 1, 31), 1, ymd(2024, 2, 29)},  // 闰年 2 月
		{ymd(2023, 1, 31), 1, ymd(2023, 2, 28)},  // 平年 2 月
		{ymd(2024, 3, 31), 1, ymd(2024, 4, 30)},  // 30 天月
		{ymd(2024, 10, 31), 4, ymd(2025, 2, 28)}, // 跨年落到平年 2 月
		{ymd(2024, 12, 15), 1, ymd(2025, 1, 15)}, // 普通跨年
		{ymd(2024, 1, 15), 12, ymd(2025, 1, 15)}, // 整年
		{ymd(2024, 1, 15), -1, ymd(2023, 12, 15)},
		{ymd(2024, 3, 31), -1, ymd(2024, 2, 29)},
		{ymd(2024, 2, 29), 12, ymd(2025, 2, 28)}, // 闰日加一年
		{ymd(2024, 8, 31), 6, ymd(2025, 2, 28)},
		{ymd(1900, 1, 31), 1, ymd(1900, 2, 28)}, // 1900 非闰年
		{ymd(2000, 1, 31), 1, ymd(2000, 2, 29)}, // 2000 闰年
	}
	for _, c := range cases {
		start := FromCivil(c.start[0], c.start[1], c.start[2])
		want := FromCivil(c.want[0], c.want[1], c.want[2])
		if got := AddMonths(start, c.n); got != want {
			gy, gm, gd := ToCivil(got)
			t.Errorf("AddMonths(%v, %d) = %v, want %v", c.start, c.n, [3]int{gy, gm, gd}, c.want)
		}
	}
}
