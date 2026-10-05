package mtm

import (
	"testing"

	"ontology/lot"
)

func TestCeilRate(t *testing.T) {
	cases := []struct {
		amount, rate, want int64
	}{
		{150000, 1, 15},       // 题面: ceil(150000/10000)=15
		{90300, 1, 10},        // 题面: ceil(9.03)=10
		{181200, 1, 19},       // 题面: ceil(18.12)=19
		{0, 5, 0},             // 零金额
		{100, 0, 0},           // 零费率
		{1, 1, 1},             // 最小正数向上取整
		{10000, 10000, 10000}, // 恰整不多加
		{1e17, 10000, 1e17},   // 大数不溢出: 1e17*1e4 超 int64
	}
	for _, c := range cases {
		if got := CeilRate(c.amount, c.rate); got != c.want {
			t.Fatalf("CeilRate(%d,%d)=%d 判据: ceil(amount*rate/10000)=%d", c.amount, c.rate, got, c.want)
		}
	}
}

func TestMargin(t *testing.T) {
	c := Contract{Mult: 10, Mr: 1000}
	// 题面: 6 手昨仓 sp0=3015 -> ceil(6*3015*10*1000/10000)=18090
	p := &lot.Position{Qy: 6, Sp0: 3015}
	if got := Margin(p, c).Int64(); got != 18090 {
		t.Fatalf("Margin=%d 判据: ceil(6*3015*10*1000/10000)=18090", got)
	}
	// 昨仓+今仓混合, 单持仓只取整一次: 价值 1*100+1*101=201, mult=1, mr=50
	// ceil(201*1*50/10000)=ceil(1.005)=2
	p2 := &lot.Position{Qy: 1, Sp0: 100, Today: []lot.Batch{{Price: 101, Qty: 1}}}
	c2 := Contract{Mult: 1, Mr: 50}
	if got := Margin(p2, c2).Int64(); got != 2 {
		t.Fatalf("Margin=%d 判据: 单持仓合并价值后取整一次 ceil(1.005)=2", got)
	}
}

func TestSettlePnL(t *testing.T) {
	c := Contract{Mult: 10}
	// 多头: 昨仓 2 手 sp0=100, 今仓 1 手@110, 结算 105
	// (105-100)*2*10 + (105-110)*1*10 = 100-50 = 50
	p := &lot.Position{Qy: 2, Sp0: 100, Today: []lot.Batch{{Price: 110, Qty: 1}}}
	if got := SettlePnL(p, lot.Long, 105, c); got != 50 {
		t.Fatalf("多头 SettlePnL=%d 判据: s=+1, 期望 50", got)
	}
	// 空头: 符号取反 -> -50
	if got := SettlePnL(p, lot.Short, 105, c); got != -50 {
		t.Fatalf("空头 SettlePnL=%d 判据: s=-1, 期望 -50", got)
	}
}
