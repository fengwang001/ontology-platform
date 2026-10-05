package subst

import (
	"math/big"
	"testing"
)

func TestPrecollectCeil(t *testing.T) {
	cases := []struct {
		d, p, prem int64
		want       int64
	}{
		{50, 21, 1050, 1161},                    // ceil(1160.25)，题面示例
		{1, 3, 1050, 4},                         // ceil(3.315)
		{1, 2, 0, 2},                            // 整除不向上
		{3, 7, 5000, 32},                        // ceil(31.5)
		{1000000, 1000000, 5000, 1500000000000}, // 大量纲不溢出
	}
	for _, c := range cases {
		if got := Precollect(c.d, c.p, c.prem); got != c.want {
			t.Errorf("Precollect(%d,%d,%d) = %d, want %d", c.d, c.p, c.prem, got, c.want)
		}
	}
}

func TestRedeemPayFloor(t *testing.T) {
	cases := []struct {
		d, p, prem int64
		want       int64
	}{
		{50, 22, 1050, 984}, // floor(984.5)，题面示例
		{1, 3, 1050, 2},     // floor(2.685)，与申购方向相反
		{1, 2, 0, 2},        // 整除
		{3, 7, 5000, 10},    // floor(10.5)
	}
	for _, c := range cases {
		if got := RedeemPay(c.d, c.p, c.prem); got != c.want {
			t.Errorf("RedeemPay(%d,%d,%d) = %d, want %d", c.d, c.p, c.prem, got, c.want)
		}
	}
}

func TestRatioOK(t *testing.T) {
	cases := []struct {
		x, y int64
		rmax int64
		want bool
	}{
		{6050, 10200, 60, true}, // 题面示例：605000<=612000
		{6260, 10200, 60, false},
		{6, 10, 60, true},  // 取等通过
		{7, 10, 60, false}, // 多 1 拒绝
		{0, 10, 0, true},   // 无替代且 Rmax=0
		{1, 10, 0, false},
	}
	for _, c := range cases {
		got := RatioOK(big.NewInt(c.x), big.NewInt(c.y), c.rmax)
		if got != c.want {
			t.Errorf("RatioOK(%d,%d,%d) = %v, want %v", c.x, c.y, c.rmax, got, c.want)
		}
	}
}

func TestSettle(t *testing.T) {
	if got := Settle(1161, 1100); got != 61 {
		t.Errorf("Settle(1161,1100) = %d, want 61（退）", got)
	}
	if got := Settle(1161, 1200); got != -39 {
		t.Errorf("Settle(1161,1200) = %d, want -39（补收）", got)
	}
}
