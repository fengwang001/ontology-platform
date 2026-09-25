package wagg

import (
	"fmt"
	"testing"

	"ontology/win"
)

// TestWindows 钉住 win.Windows 的重叠枚举（含负时间戳）与 Fired 判定。
func TestWindows(t *testing.T) {
	cases := []struct {
		ts, size, hop int64
		want          []win.Window
	}{
		{6, 8, 4, []win.Window{{Start: 0, End: 8}, {Start: 4, End: 12}}},
		{12, 8, 4, []win.Window{{Start: 8, End: 16}, {Start: 12, End: 20}}},
		{0, 8, 4, []win.Window{{Start: -4, End: 4}, {Start: 0, End: 8}}},
		{-5, 8, 4, []win.Window{{Start: -12, End: -4}, {Start: -8, End: 0}}},
		{-8, 8, 4, []win.Window{{Start: -12, End: -4}, {Start: -8, End: 0}}},
		{12, 10, 5, []win.Window{{Start: 5, End: 15}, {Start: 10, End: 20}}},
		{5, 6, 2, []win.Window{{Start: 0, End: 6}, {Start: 2, End: 8}, {Start: 4, End: 10}}},
	}
	for _, c := range cases {
		got := win.Windows(c.ts, c.size, c.hop)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("Windows(%d,%d,%d)=%v, want %v", c.ts, c.size, c.hop, got, c.want)
		}
	}
	if !win.Fired(8, 8) || win.Fired(8, 7) {
		t.Error("Fired 必须是 wm >= end")
	}
}

// TestTriggerCheckBound 证明按窗口 end 有序定位：水位线推进的检查数不随
// 未触发窗口数 m 线性增长，不超过一个小常数加上本次真正触发的窗口数。
func TestTriggerCheckBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, err := New(8, 4, 1<<60) // 巨大 delay：窗口全部保持未触发
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{Key: fmt.Sprintf("k%d", i), TS: 0}
		}
		if _, err := a.Feed(evs); err != nil {
			t.Fatal(err)
		}
		cs, err := a.Feed([]Event{{Key: "z", TS: 4}}) // 只推进水位线，不跨过任何 end
		if err != nil {
			t.Fatal(err)
		}
		if a.lastChecks > 1+len(cs) {
			t.Fatalf("m=%d: 检查 %d 次，超过 1+%d（整表扫描？）", m, a.lastChecks, len(cs))
		}
	}
	// 有真实触发时：检查数 <= 触发数 + 1
	a, _ := New(8, 4, 0)
	for ts := int64(0); ts < 40; ts += 4 {
		cs, err := a.Feed([]Event{{Key: "k", TS: ts}})
		if err != nil {
			t.Fatal(err)
		}
		if a.lastChecks > len(cs)+1 {
			t.Fatalf("ts=%d: 检查 %d 次，触发 %d 个", ts, a.lastChecks, len(cs))
		}
	}
}
