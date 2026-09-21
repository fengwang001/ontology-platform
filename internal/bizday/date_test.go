package bizday

import "testing"

func TestIsLeap(t *testing.T) {
	cases := map[int]bool{
		1900: false, // 整百但不整 400
		2000: true,  // 整 400
		2024: true,
		2026: false,
		2028: true,
		2100: false,
	}
	for y, want := range cases {
		if got := isLeap(y); got != want {
			t.Errorf("isLeap(%d) = %v, want %v", y, got, want)
		}
	}
}

func TestValidDate(t *testing.T) {
	valid := []int{19000101, 29991231, 20260921, 20280229, 20000229}
	for _, d := range valid {
		if !validDate(d) {
			t.Errorf("validDate(%d) = false, want true", d)
		}
	}
	invalid := []int{
		18991231, 30000101, // 年份越界
		20260230, 20261301, 20260931, // 月日非法
		20260229,           // 非闰年 2 月 29 日
		19000229,           // 整百非闰
		20260001, 20260100, // 零月零日
	}
	for _, d := range invalid {
		if validDate(d) {
			t.Errorf("validDate(%d) = true, want false", d)
		}
	}
}

func TestWeekday(t *testing.T) {
	// 0=周日 … 6=周六
	cases := map[int]int{
		19000101: 1, // 周一（基准）
		20260916: 3, // 周三
		20260918: 5, // 周五
		20260919: 6, // 周六
		20260920: 0, // 周日
		20260921: 1, // 周一
		20000229: 2, // 周二（闰日）
		20280229: 2, // 周二（闰日）
	}
	for d, want := range cases {
		if got := weekday(d); got != want {
			t.Errorf("weekday(%d) = %d, want %d", d, got, want)
		}
	}
}

func TestSerialRoundTrip(t *testing.T) {
	for _, d := range []int{19000101, 20260921, 20280229, 29991231} {
		y, m, day := split(d)
		s := toSerial(y, m, day)
		if got := join(fromSerial(s)); got != d {
			t.Errorf("roundTrip(%d) = %d", d, got)
		}
	}
	// 相邻序列号对应相邻日期（跨月跨年）
	pairs := [][2]int{
		{20260930, 20261001},
		{20261231, 20270101},
		{20280228, 20280229},
		{20280229, 20280301},
		{20270228, 20270301}, // 非闰年
	}
	for _, p := range pairs {
		y0, m0, d0 := split(p[0])
		y1, m1, d1 := split(p[1])
		if toSerial(y1, m1, d1)-toSerial(y0, m0, d0) != 1 {
			t.Errorf("serial gap %d -> %d is not 1", p[0], p[1])
		}
	}
}
