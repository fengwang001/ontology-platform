package tidx

import (
	"fmt"
	"testing"
)

// TestAddRule 表驱动核验追加规则：首条必记；仅严格大于当前最大值才记；相等不记。
func TestAddRule(t *testing.T) {
	cases := []struct {
		name       string
		ts         []int64
		wantCount  int
		wantLastTS int64
	}{
		{"first always recorded", []int64{50}, 1, 50},
		{"strictly increasing all recorded", []int64{1, 2, 3}, 3, 3},
		{"equal to max not recorded", []int64{70, 70}, 1, 70},
		{"lower then rebound", []int64{50, 40, 70, 60, 70, 65}, 2, 70},
		{"ten-message sequence", []int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85}, 3, 90},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ix := New()
			for k, v := range c.ts {
				ix.Add(v, int64(100+k))
			}
			if len(ix.items) != c.wantCount {
				t.Fatalf("entries=%d want %d", len(ix.items), c.wantCount)
			}
			if ix.items[len(ix.items)-1].TS != c.wantLastTS {
				t.Fatalf("last TS=%d want %d", ix.items[len(ix.items)-1].TS, c.wantLastTS)
			}
		})
	}
}

// TestLookupLowerBound 表驱动核验索引上的下界语义。
func TestLookupLowerBound(t *testing.T) {
	ix := New()
	vals := []struct{ ts, off int64 }{{50, 100}, {70, 102}, {90, 106}}
	for _, v := range vals {
		ix.Add(v.ts, v.off)
	}
	cases := []struct {
		t        int64
		wantOff  int64
		wantFind bool
	}{
		{49, 100, true}, {50, 100, true}, {51, 102, true},
		{70, 102, true}, {85, 106, true}, {90, 106, true},
		{91, 0, false},
	}
	for _, c := range cases {
		if off, ok := ix.Lookup(c.t); off != c.wantOff || ok != c.wantFind {
			t.Errorf("Lookup(%d)=(%d,%v) want (%d,%v)", c.t, off, ok, c.wantOff, c.wantFind)
		}
	}
	if off, ok := New().Lookup(0); ok || off != 0 {
		t.Errorf("empty index Lookup=(%d,%v) want (0,false)", off, ok)
	}
}

// TestTidxProbeBound 核验复杂度：m 档 100/1000/10000 严格递增序列（恰 m 个索引项），
// 对小于全部/大于全部/恰好命中/两项之间四类 t 查询，探查数 <= 2*ceil(log2(m+1))+4，不随 m 线性增长。
func TestTidxProbeBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			ix := New()
			for i := 0; i < m; i++ {
				ix.Add(int64(i)*2, int64(i))
			}
			if len(ix.items) != m {
				t.Fatalf("index items=%d want %d", len(ix.items), m)
			}
			bound := 2*ceilLog2(m+1) + 4
			ts := []int64{-1, int64(m)*2 + 1, 50, 51}
			for _, q := range ts {
				ix.Lookup(q)
				if p := int(ix.probes.Load()); p > bound {
					t.Errorf("m=%d t=%d probes=%d > bound %d", m, q, p, bound)
				}
			}
		})
	}
}
