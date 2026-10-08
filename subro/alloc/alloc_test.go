package alloc

import "testing"

func TestCapFloor(t *testing.T) {
	cases := []struct {
		totalLoss, ratioBP, want int64
	}{
		{10000, 10000, 10000}, // 全责
		{10000, 5000, 5000},   // 半责
		{10000, 0, 0},         // 无责
		{9999, 10000, 9999},
		{3, 3333, 0},          // 3*3333/10000 = 0.9999 向下取整
		{10001, 5000, 5000},   // 10001*5000/10000 = 5000.5 向下取整
		{1 << 60, 10000, 1 << 60}, // 大数不溢出
		{1<<60 - 1, 9999, 1152921504606846975 * 9999 / 10000},
	}
	for _, tc := range cases {
		if got := Cap(tc.totalLoss, tc.ratioBP); got != tc.want {
			t.Errorf("Cap(%d,%d)=%d, 期望 %d", tc.totalLoss, tc.ratioBP, got, tc.want)
		}
	}
}

func TestComputeInvariant(t *testing.T) {
	// 任何时刻三方应得之和等于净回收总额。
	for _, net := range []int64{0, 1, 5000, 9999, 20000} {
		for _, waived := range []bool{false, true} {
			a := Compute(net, 8000, 4000, 6000, waived)
			sum := a.Insured + a.Insurer + a.ThirdParty
			if sum != net {
				t.Errorf("net=%d waived=%v: 应得之和 %d != 净回收总额", net, waived, sum)
			}
			if a.Insured > 4000 && !waived {
				t.Errorf("被保险人应得不得超过未获赔额: %+v", a)
			}
			if waived && a.Insured != 0 {
				t.Errorf("放弃后被保险人应得应为零: %+v", a)
			}
			if a.Insurer > 6000 {
				t.Errorf("保险人应得不得超过已赔付额: %+v", a)
			}
		}
	}
}
