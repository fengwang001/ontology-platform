package dedup

import (
	"fmt"
	"testing"
)

// TestProbeConstant 证明定位已存在 ID 的检查个数不随表规模 m 线性增长：
// 先保留 m 个不同 ID，再 Dedup 一个已存在 ID，probes 必须 <= 小常数。
func TestProbeConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			tab := New(5, m+1)
			for i := 0; i < m; i++ {
				tab.Dedup(fmt.Sprintf("id%d", i), int64(i*10))
			}
			if tab.Dedup("id0", 1) { // 乱序重复，触发定位
				t.Fatal("out-of-order event must be duplicate")
			}
			if tab.probes > 2 {
				t.Fatalf("probes=%d grows with m=%d, want constant", tab.probes, m)
			}
		})
	}
}

// TestTableRules 表驱动：重复/乱序不刷新 last、不触发淘汰；淘汰选 last 最小者。
func TestTableRules(t *testing.T) {
	type step struct {
		id   string
		ts   int64
		want bool
	}
	cases := []struct {
		name string
		w    int64
		max  int
		seq  []step
		view map[string]int64
		dup  int64
	}{
		{"dup keeps last", 5, 3, []step{{"A", 10, true}, {"A", 14, false}, {"A", 15, true}},
			map[string]int64{"A": 15}, 1},
		{"out of order", 5, 3, []step{{"A", 10, true}, {"A", 10, false}, {"A", 3, false}, {"A", 15, true}},
			map[string]int64{"A": 15}, 2},
		{"evict smallest last", 5, 2, []step{{"A", 10, true}, {"B", 20, true}, {"C", 30, true}},
			map[string]int64{"B": 20, "C": 30}, 0},
		{"evicted is new again", 5, 2, []step{{"A", 10, true}, {"B", 20, true}, {"C", 30, true}, {"A", 11, true}},
			map[string]int64{"C": 30, "A": 11}, 0},
		{"dup never evicts", 5, 2, []step{{"A", 10, true}, {"B", 20, true}, {"A", 12, false}, {"B", 22, false}},
			map[string]int64{"A": 10, "B": 20}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab := New(c.w, c.max)
			for i, s := range c.seq {
				if got := tab.Dedup(s.id, s.ts); got != s.want {
					t.Fatalf("step %d (%s,%d): got %v want %v", i, s.id, s.ts, got, s.want)
				}
			}
			v := tab.View()
			if len(v) != len(c.view) || len(v) > c.max {
				t.Fatalf("view=%v want %v", v, c.view)
			}
			for id, last := range c.view {
				if v[id] != last {
					t.Fatalf("view[%s]=%d want %d", id, v[id], last)
				}
			}
			if tab.Duplicated() != c.dup {
				t.Fatalf("dup=%d want %d", tab.Duplicated(), c.dup)
			}
		})
	}
}
