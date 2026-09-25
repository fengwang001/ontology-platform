package dedup

import (
	"fmt"
	"testing"

	"ontology/win"
)

// TestWindowBoundary 钉住不变量 1：半开窗口 [last, last+W)，d==W 接受。
// 每行 {last, ts, w, dup}：d==W 接受；d<W、相等、更旧乱序均重复。
func TestWindowBoundary(t *testing.T) {
	cases := [][4]int64{
		{10, 15, 5, 0}, {10, 14, 5, 1}, {10, 10, 5, 1}, {10, 9, 5, 1},
		{0, 4, 5, 1}, {0, 5, 5, 0}, {100, 200, 5, 0}, {100, 104, 5, 1},
	}
	for _, c := range cases {
		if got := win.Duplicate(c[0], c[1], c[2]); got != (c[3] == 1) {
			t.Errorf("last=%d ts=%d w=%d: dup=%v, want %v", c[0], c[1], c[2], got, c[3] == 1)
		}
	}
}

// TestLookupIsConstant 证明去重表用 map 定位：保留 m 个不同 ID 后，
// Dedup 一个已存在 ID 所检查的表条目数不随 m 线性增长（恒为小常数）。
// 包内测试直接读非导出字段 checked，不经任何导出接口。
func TestLookupIsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			tb, err := New(10, m)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < m; i++ {
				if _, err := tb.Dedup(fmt.Sprintf("id%d", i), int64(i)*10); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tb.Dedup("id0", 1); err != nil { // 已存在，判重复
				t.Fatal(err)
			}
			if tb.checked > 2 {
				t.Fatalf("m=%d: checked %d entries, want <= 2 (map lookup, not scan)", m, tb.checked)
			}
		})
	}
}

// TestEvictOldest 表满时淘汰 last 最小者；重复不触发淘汰。
func TestEvictOldest(t *testing.T) {
	tb, err := New(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		id string
		ts int64
	}{{"a", 10}, {"b", 20}} {
		if ok, _ := tb.Dedup(e.id, e.ts); !ok {
			t.Fatalf("%v not accepted", e)
		}
	}
	if ok, _ := tb.Dedup("a", 12); ok { // 重复：不刷新 last、不触发淘汰
		t.Fatal("a@12 should be duplicate")
	}
	if ok, _ := tb.Dedup("c", 30); !ok {
		t.Fatal("c@30 should be accepted")
	}
	view := tb.View()
	if _, ok := view["a"]; ok {
		t.Fatalf("oldest id a should be evicted, view=%v", view)
	}
	if len(view) != 2 || view["b"] != 20 || view["c"] != 30 {
		t.Fatalf("bad view after eviction: %v", view)
	}
	if tb.Accepted() != 3 || tb.Duplicated() != 1 {
		t.Fatalf("counters: accepted=%d duplicated=%d", tb.Accepted(), tb.Duplicated())
	}
}
