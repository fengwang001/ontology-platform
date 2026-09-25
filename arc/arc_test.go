package arc

import "testing"

// TestEvictCheckCount 钉住复杂度约束：m 条归档驱逐其中 k 条，
// 检查个数不得超过 k+1（从头部逐条到第一个幸存者即停），而非扫全 m 条。
func TestEvictCheckCount(t *testing.T) {
	cases := []struct{ m, k int }{
		{100, 0}, {100, 1}, {100, 50}, {100, 99}, {100, 100},
		{1000, 1}, {1000, 500}, {1000, 1000},
		{10000, 1}, {10000, 9999}, {10000, 10000},
	}
	for _, c := range cases {
		var a Archive
		for i := 0; i < c.m; i++ { // ts 单调递增：0..m-1
			a.Append(int64(i+1), int64(i))
		}
		a.Evict(int64(c.k)) // 驱逐 ts < k，恰好 k 条
		if a.checked > c.k+1 {
			t.Errorf("m=%d k=%d: checked=%d > k+1=%d", c.m, c.k, a.checked, c.k+1)
		}
		if a.Len() != c.m-c.k {
			t.Errorf("m=%d k=%d: Len=%d want %d", c.m, c.k, a.Len(), c.m-c.k)
		}
	}
}

// TestAppendEvictMax 表驱动钉住追加/驱逐/最大位点的基本语义与严格小于边界。
func TestAppendEvictMax(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
		cutoff  int64
		wantLen int
		wantMax int64
		wantOk  bool
	}{
		{"empty", nil, 10, 0, 0, false},
		{"strict-boundary-keeps-equal", []Entry{{110, 10}}, 10, 1, 110, true},
		{"evict-head-only", []Entry{{1, 0}, {2, 5}, {3, 9}}, 5, 2, 3, true},
		{"evict-all", []Entry{{1, 0}, {2, 5}}, 6, 0, 0, false},
		{"evict-none", []Entry{{1, 7}, {2, 8}}, 7, 2, 2, true},
	}
	for _, c := range cases {
		var a Archive
		for _, e := range c.entries {
			a.Append(e.Off, e.Ts)
		}
		a.Evict(c.cutoff)
		if a.Len() != c.wantLen {
			t.Errorf("%s: Len=%d want %d", c.name, a.Len(), c.wantLen)
		}
		if off, ok := a.Max(); off != c.wantMax || ok != c.wantOk {
			t.Errorf("%s: Max=(%d,%v) want (%d,%v)", c.name, off, ok, c.wantMax, c.wantOk)
		}
	}
}
