package quote

import "testing"

func TestQualified(t *testing.T) {
	cases := []struct {
		name         string
		st           State
		qmin, spread int64
		want         bool
	}{
		{
			name: "价差取等合格",
			// (10050-9950)*20000 = 2_000_000 = 100*(10050+9950)
			st:   State{Has: true, Bid: 9950, Ask: 10050, BidQty: 20, AskQty: 20},
			qmin: 10, spread: 100,
			want: true,
		},
		{
			name: "价差多1不合格",
			// 101*20000 = 2_020_000 > 100*20001 = 2_000_100
			st:   State{Has: true, Bid: 9950, Ask: 10051, BidQty: 20, AskQty: 20},
			qmin: 10, spread: 100,
			want: false,
		},
		{
			name: "买侧量等于下限合格",
			st:   State{Has: true, Bid: 100, Ask: 101, BidQty: 10, AskQty: 10},
			qmin: 10, spread: 10000,
			want: true,
		},
		{
			name: "买侧量少1不合格",
			st:   State{Has: true, Bid: 100, Ask: 101, BidQty: 9, AskQty: 10},
			qmin: 10, spread: 10000,
			want: false,
		},
		{
			name: "卖侧量少1不合格",
			st:   State{Has: true, Bid: 100, Ask: 101, BidQty: 10, AskQty: 9},
			qmin: 10, spread: 10000,
			want: false,
		},
		{
			name: "无报价不合格",
			st:   State{},
			qmin: 1, spread: 10000,
			want: false,
		},
		{
			name: "零价差合格",
			st:   State{Has: true, Bid: 100, Ask: 100, BidQty: 1, AskQty: 1},
			qmin: 1, spread: 1,
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.st.Qualified(c.qmin, c.spread); got != c.want {
				t.Fatalf("Qualified()=%v, want %v", got, c.want)
			}
		})
	}
}

func TestSetClearFill(t *testing.T) {
	var s State
	if s.Clear() {
		t.Fatal("无报价时 Clear 应返回 false")
	}
	if s.Fill(Buy, 1) {
		t.Fatal("无报价时 Fill 应返回 false")
	}
	s.Set(100, 20, 101, 30)
	if !s.Has || s.Bid != 100 || s.BidQty != 20 || s.Ask != 101 || s.AskQty != 30 {
		t.Fatalf("Set 后状态不符: %+v", s)
	}
	if s.Fill(Buy, 21) {
		t.Fatal("Fill 超过买侧数量应返回 false")
	}
	if s.Fill(Sell, 31) {
		t.Fatal("Fill 超过卖侧数量应返回 false")
	}
	if !s.Fill(Buy, 15) || s.BidQty != 5 {
		t.Fatalf("Fill 买侧后数量应为 5: %+v", s)
	}
	if !s.Fill(Sell, 30) || s.AskQty != 0 {
		t.Fatalf("Fill 卖侧后数量应为 0: %+v", s)
	}
	s.Set(200, 1, 201, 2) // 整体替换
	if s.Bid != 200 || s.BidQty != 1 || s.AskQty != 2 {
		t.Fatalf("整体替换后状态不符: %+v", s)
	}
	if !s.Clear() || s.Has {
		t.Fatal("Clear 应成功并清空报价")
	}
	if s.Clear() {
		t.Fatal("重复 Clear 应返回 false")
	}
}
