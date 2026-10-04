package obligation

import "testing"

func TestWindowsAddAndOverlap(t *testing.T) {
	var w Windows
	if !w.Add(100, 200) {
		t.Fatal("首个窗口应登记成功")
	}
	if !w.Add(200, 300) {
		t.Fatal("首尾相接不算重叠，应登记成功")
	}
	if !w.Add(50, 100) {
		t.Fatal("前向相接也不算重叠，应登记成功")
	}
	for _, bad := range [][2]int64{{99, 101}, {150, 250}, {250, 400}, {0, 51}, {100, 200}} {
		if w.Add(bad[0], bad[1]) {
			t.Fatalf("窗口 %v 与已有窗口重叠，应报冲突", bad)
		}
	}
	cases := []struct {
		lo, hi int64
		want   int64
	}{
		{0, 50, 0},
		{0, 100, 50},
		{60, 90, 30},
		{100, 300, 200},
		{150, 250, 100},
		{0, 1000, 250},
		{300, 400, 0},
	}
	for _, c := range cases {
		if got := w.Overlap(c.lo, c.hi); got != c.want {
			t.Fatalf("Overlap(%d,%d)=%d, want %d", c.lo, c.hi, got, c.want)
		}
	}
}

type qualEvent struct {
	t    int64
	qual bool
}

// simulate 逐秒朴素模拟：每秒结束时按当前合格态累计时段内秒数。
func simulate(open, close int64, events []qualEvent, end int64) int64 {
	qual := false
	var acc int64
	ei := 0
	for s := int64(0); s < end; s++ {
		for ei < len(events) && events[ei].t == s {
			qual = events[ei].qual
			ei++
		}
		day := s / SecPerDay
		if qual && s >= day*SecPerDay+open && s < day*SecPerDay+close {
			acc++
		}
	}
	return acc
}

func TestTrackerAccrualVsNaive(t *testing.T) {
	cases := []struct {
		name        string
		open, close int64
		regT        int64
		events      []qualEvent
		settleDay   int64
	}{
		{"时段内全程合格", 100, 200, 0, []qualEvent{{50, true}}, 0},
		{"登记晚于开盘", 100, 200, 150, []qualEvent{{150, true}}, 0},
		{"中途变不合格", 100, 200, 0, []qualEvent{{100, true}, {150, false}}, 0},
		{"时段外不计", 100, 200, 0, []qualEvent{{0, true}, {250, false}}, 0},
		{"跨日状态延续", 100, 200, 0, []qualEvent{{150, true}}, 1},
		{"跨日中途变化", 100, 200, 0, []qualEvent{{150, true}, {SecPerDay + 120, false}}, 1},
		{"空日按当前态计整天", 100, 200, 0, []qualEvent{{150, true}}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var w Windows
			tr := NewTracker(c.open, c.close, 0, Day(c.regT), c.regT)
			for _, ev := range c.events {
				tr.OnQuote(ev.t, 0, ev.qual, &w)
			}
			end := c.settleDay*SecPerDay + c.close
			want := simulate(c.open, c.close, c.events, end)
			// 逐日结算，核对每天与累计总量。
			var got int64
			for d := Day(c.regT); d <= c.settleDay; d++ {
				got += tr.SettleDay(d, d, &w)
			}
			if got != want {
				t.Fatalf("累计合格时长=%d, 逐秒模拟=%d", got, want)
			}
		})
	}
}

func TestTrackerGrace(t *testing.T) {
	newTr := func() (*Tracker, *Windows) {
		w := &Windows{}
		return NewTracker(100, 1000, 5, 0, 0), w
	}
	t.Run("宽限恰在t0+G恢复", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(400, 0, true, false, w) // t0=400
		tr.OnQuote(405, 0, true, w)       // 405<=405，[400,405) 补记
		if got := tr.SettleDay(0, 0, w); got != 900 {
			t.Fatalf("A=%d, want 900", got)
		}
	})
	t.Run("晚1秒整段作废", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(400, 0, true, false, w)
		tr.OnQuote(406, 0, true, w) // 406>405，[400,406) 整段不合格
		if got := tr.SettleDay(0, 0, w); got != 894 {
			t.Fatalf("A=%d, want 894", got)
		}
	})
	t.Run("宽限中Withdraw作废", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(400, 0, true, false, w)
		tr.OnWithdraw(402, 0, w)
		tr.OnQuote(404, 0, true, w) // 宽限已作废，不补记
		if got := tr.SettleDay(0, 0, w); got != 896 {
			t.Fatalf("A=%d, want 896", got)
		}
	})
	t.Run("宽限跨收盘作废", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(998, 0, true, false, w) // t0=998，G=5
		tr.OnQuote(1001, 0, true, w)      // 已跨收盘，不补记
		if got := tr.SettleDay(0, 0, w); got != 898 {
			t.Fatalf("A=%d, want 898", got)
		}
	})
	t.Run("宽限中再次Fill不改t0", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(400, 0, true, false, w) // t0=400
		tr.OnFill(403, 0, false, false, w)
		tr.OnQuote(405, 0, true, w) // 仍按 t0=400 判定，补记成功
		if got := tr.SettleDay(0, 0, w); got != 900 {
			t.Fatalf("A=%d, want 900", got)
		}
	})
	t.Run("宽限补记扣除豁免", func(t *testing.T) {
		tr, w := newTr()
		tr.OnQuote(100, 0, true, w)
		tr.OnFill(400, 0, true, false, w)
		if !w.Add(402, 404) {
			t.Fatal("豁免登记失败")
		}
		tr.OnQuote(405, 0, true, w) // 补记 [400,405) 但 [402,404) 豁免
		if got := tr.SettleDay(0, 0, w); got != 898 {
			t.Fatalf("A=%d, want 898", got)
		}
	})
}
