package obligation

import (
	"testing"

	"ontology/quote"
)

func TestQualified(t *testing.T) {
	cases := []struct {
		name    string
		q       quote.Quote
		has     bool
		qmin, s int64
		want    bool
	}{
		{"价差取等合格", quote.Quote{Bid: 9950, BidQty: 20, Ask: 10050, AskQty: 20}, true, 10, 100, true},
		{"价差多1不合格", quote.Quote{Bid: 9950, BidQty: 20, Ask: 10051, AskQty: 20}, true, 10, 100, false},
		{"量取等合格", quote.Quote{Bid: 9950, BidQty: 10, Ask: 10050, AskQty: 10}, true, 10, 100, true},
		{"买侧量少1", quote.Quote{Bid: 9950, BidQty: 9, Ask: 10050, AskQty: 20}, true, 10, 100, false},
		{"卖侧量少1", quote.Quote{Bid: 9950, BidQty: 20, Ask: 10050, AskQty: 9}, true, 10, 100, false},
		{"无报价", quote.Quote{Bid: 9950, BidQty: 20, Ask: 10050, AskQty: 20}, false, 10, 100, false},
	}
	for _, c := range cases {
		if got := Qualified(c.q, c.has, c.qmin, c.s); got != c.want {
			t.Errorf("%s: Qualified=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestWindows(t *testing.T) {
	var w Windows
	// 首尾相接不算重叠
	for _, iv := range [][2]int64{{800, 900}, {900, 1000}, {700, 800}, {2000, 2100}} {
		if !w.Add(iv[0], iv[1]) {
			t.Fatalf("Add(%d,%d) 应成功（首尾相接）", iv[0], iv[1])
		}
	}
	// 重叠报冲突
	for _, iv := range [][2]int64{{850, 950}, {750, 801}, {899, 901}, {600, 3000}, {800, 900}} {
		if w.Add(iv[0], iv[1]) {
			t.Fatalf("Add(%d,%d) 应冲突", iv[0], iv[1])
		}
	}
	cases := []struct {
		a, b int64
		want int64
	}{
		{0, 100, 0},
		{700, 800, 100},
		{750, 850, 100},
		{800, 1000, 200},
		{0, 3000, 400},
		{850, 860, 10},
		{1000, 2000, 0},
	}
	for _, c := range cases {
		if got := w.Intersection(c.a, c.b); got != c.want {
			t.Errorf("Intersection(%d,%d)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func newTestTracker() *Tracker {
	p := Params{Open: 100, Close: 1100, Qmin: 10, S: 100, G: 5}
	return NewTracker(p, &Windows{}, 0)
}

var goodQuote = quote.Quote{Bid: 9950, BidQty: 20, Ask: 10050, AskQty: 20}

func settleA(tr *Tracker, day int64) int64 {
	tr.Sync(day*DayLen + 1100)
	return tr.A(day)
}

func TestTrackerAccumulate(t *testing.T) {
	cases := []struct {
		name string
		run  func(tr *Tracker)
		want int64
	}{
		{"全程合格", func(tr *Tracker) { tr.OnQuote(100, goodQuote) }, 1000},
		{"时段外不计", func(tr *Tracker) { tr.OnQuote(0, goodQuote) }, 1000},
		{"中途撤单", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnWithdraw(600)
		}, 500},
		{"宽限恰在t0+G恢复", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(400, quote.Bid, 15)
			tr.OnQuote(405, goodQuote)
		}, 1000},
		{"宽限晚1秒整段作废", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(400, quote.Bid, 15)
			tr.OnQuote(406, goodQuote)
		}, 994},
		{"宽限中Withdraw作废", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(400, quote.Bid, 15)
			tr.OnWithdraw(402)
			tr.OnQuote(403, goodQuote)
		}, 997},
		{"主动改量无宽限", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnQuote(400, quote.Quote{Bid: 9950, BidQty: 5, Ask: 10050, AskQty: 20})
			tr.OnQuote(401, goodQuote)
		}, 999},
		{"宽限跨收盘作废", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(1098, quote.Bid, 15)
			tr.OnQuote(1103, goodQuote)
		}, 998},
		{"宽限中再Fill不改t0", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(400, quote.Bid, 15)
			tr.OnFill(402, quote.Bid, 1)
			tr.OnQuote(405, goodQuote)
		}, 1000},
		{"宽限中不合格Quote不改t0", func(tr *Tracker) {
			tr.OnQuote(100, goodQuote)
			tr.OnFill(400, quote.Bid, 15)
			tr.OnQuote(402, quote.Quote{Bid: 9950, BidQty: 5, Ask: 10050, AskQty: 20})
			tr.OnQuote(405, goodQuote)
		}, 1000},
	}
	for _, c := range cases {
		tr := newTestTracker()
		c.run(tr)
		if got := settleA(tr, 0); got != c.want {
			t.Errorf("%s: A=%d, want %d", c.name, got, c.want)
		}
	}
}

func TestTrackerExemptDeduct(t *testing.T) {
	w := &Windows{}
	p := Params{Open: 100, Close: 1100, Qmin: 10, S: 100, G: 5}
	tr := NewTracker(p, w, 0)
	tr.OnQuote(100, goodQuote)
	// 豁免窗口在事件到来前登记，累计时直接扣除
	w.Add(800, 900)
	if got := settleA(tr, 0); got != 900 {
		t.Errorf("A=%d, want 900（[100,1100) 扣 [800,900)）", got)
	}
}

func TestTrackerGraceCreditDeductsExempt(t *testing.T) {
	w := &Windows{}
	p := Params{Open: 100, Close: 1100, Qmin: 10, S: 100, G: 10}
	tr := NewTracker(p, w, 0)
	tr.OnQuote(100, goodQuote)
	tr.OnFill(400, quote.Bid, 15) // t0=400
	w.Add(402, 404)               // 宽限段内的豁免窗口
	tr.OnQuote(410, goodQuote)    // 410<=410 补记 [400,410) 扣 [402,404)
	if got := settleA(tr, 0); got != 998 {
		t.Errorf("A=%d, want 998（补记 10 秒中扣除 2 秒豁免）", got)
	}
}

func TestTrackerCrossDay(t *testing.T) {
	tr := newTestTracker()
	tr.OnQuote(100, goodQuote)    // 第 0 日起持续合格
	tr.OnWithdraw(2*DayLen + 600) // 第 2 日 [100,600) 合格
	tr.Sync(2*DayLen + 1100)      // 跨到第 2 日收盘后
	if got := tr.A(0); got != 1000 {
		t.Errorf("day0 A=%d, want 1000", got)
	}
	if got := tr.A(1); got != 1000 {
		t.Errorf("day1 A=%d, want 1000", got)
	}
	if got := tr.A(2); got != 500 {
		t.Errorf("day2 A=%d, want 500", got)
	}
}
