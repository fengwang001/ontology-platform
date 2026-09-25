package wagg

import (
	"fmt"
	"math/rand"
	"testing"
)

func feed(t *testing.T, a *Aggregator, evs ...Event) {
	t.Helper()
	if err := a.Apply(evs); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// 不变量2：sum 恒等于窗口内成员 Val 之和，成员全部在窗口内。
func TestMemberConservation(t *testing.T) {
	cases := []struct {
		name string
		size int64
		evs  []Event
		want map[string]int64
		drop int64
	}{
		{"七步序列", 10, []Event{{"k", 5, 10}, {"k", 15, 20}, {"k", 12, 30}, {"k", 12, 40},
			{"k", 25, 50}, {"k", 15, 60}, {"k", 35, 70}}, map[string]int64{"k": 70}, 1},
		{"同TS不去重", 10, []Event{{"k", 12, 30}, {"k", 12, 40}}, map[string]int64{"k": 70}, 0},
		{"左边界恰好过期", 10, []Event{{"k", 5, 10}, {"k", 15, 20}}, map[string]int64{"k": 20}, 0},
		{"左边界不入窗", 10, []Event{{"k", 25, 50}, {"k", 15, 60}}, map[string]int64{"k": 50}, 1},
		{"多Key独立", 10, []Event{{"a", 1, 1}, {"b", 100, 2}, {"a", 5, 3}}, map[string]int64{"a": 4, "b": 2}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := New(c.size, 0)
			if err != nil {
				t.Fatal(err)
			}
			feed(t, a, c.evs...)
			view, drop := a.Snapshot()
			if drop != c.drop {
				t.Errorf("dropped=%d want %d", drop, c.drop)
			}
			for key, want := range c.want {
				w := a.keys[key]
				var sum int64
				for _, e := range w.evs {
					sum += e.Val
					if e.TS <= w.wm-c.size {
						t.Errorf("成员 %+v 已过期却仍在窗口内", e)
					}
				}
				if sum != w.sum || w.sum != want || view[key] != want {
					t.Errorf("key %s: 成员和=%d sum=%d view=%d want=%d", key, sum, w.sum, view[key], want)
				}
			}
		})
	}
}

// 不变量3：wm 只进不退，恒等于迄今见过的最大 TS（含被丢弃事件）。
func TestWatermarkMonotonic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 5, 50, 200} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			a, _ := New(7, 0)
			var maxTS int64
			for i := 0; i < n; i++ {
				e := Event{Key: "k", TS: rng.Int63n(100), Val: 1}
				feed(t, a, e)
				maxTS = max(maxTS, e.TS)
				if w := a.keys["k"]; w.wm != maxTS {
					t.Fatalf("第%d条后 wm=%d，迄今最大TS=%d", i, w.wm, maxTS)
				}
			}
		})
	}
}

// 复杂度：检查个数不随窗口内事件数 m 增长，且不超过本次过期数 k 加常数。
func TestProbeComplexity(t *testing.T) {
	const base = int64(1) << 40
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			a, _ := New(1<<30, 0)
			evs := make([]Event, m)
			for i := range evs {
				evs[i] = Event{Key: "k", TS: base + int64(i), Val: 1}
			}
			feed(t, a, evs...)
			feed(t, a, Event{Key: "k", TS: base + int64(m), Val: 1}) // wm 只前进 1，无过期
			if a.lastProbe > 2 {
				t.Fatalf("wm 小步前进检查了 %d 个事件，随 m 线性增长", a.lastProbe)
			}
			k := m / 2
			feed(t, a, Event{Key: "k", TS: base + 1<<30 + int64(k) - 1, Val: 1}) // 恰好过期 k 个
			if a.lastProbe > k+1 {
				t.Fatalf("过期 %d 个却检查了 %d 个", k, a.lastProbe)
			}
		})
	}
}
