package cuckoo

import (
	"errors"
	"testing"
)

// TestTableFullAtomic 钉住不变量 3：n=4 插入 0..7 后 Insert(8) 驱逐成环，
// 必须返回 ErrTableFull 且两张表与插入前完全一致。
func TestTableFullAtomic(t *testing.T) {
	tab := NewTable(4, 10)
	for x := 0; x < 8; x++ {
		if err := tab.Insert(x); err != nil {
			t.Fatalf("Insert(%d): %v", x, err)
		}
	}
	want1 := []int{4, 5, 6, 7}
	want2 := []int{1, 0, 3, 2}
	got1, got2 := tab.Snapshot()
	for i := range want1 {
		if !got1[i].Occupied || got1[i].Key != want1[i] {
			t.Fatalf("T1[%d] = %+v, want key %d", i, got1[i], want1[i])
		}
		if !got2[i].Occupied || got2[i].Key != want2[i] {
			t.Fatalf("T2[%d] = %+v, want key %d", i, got2[i], want2[i])
		}
	}
	if err := tab.Insert(8); !errors.Is(err, ErrTableFull) {
		t.Fatalf("Insert(8) = %v, want ErrTableFull", err)
	}
	a1, a2 := tab.Snapshot()
	for i := range want1 {
		if a1[i] != got1[i] || a2[i] != got2[i] {
			t.Fatalf("slot %d changed after ErrTableFull: %+v/%+v", i, a1[i], a2[i])
		}
	}
	if tab.Len() != 8 {
		t.Fatalf("Len = %d, want 8 after failed insert", tab.Len())
	}
}

// TestLookupProbesConstant 钉住复杂度约束：无论已插入多少键，
// 一次 Lookup 检查的槽位数恒为 2（O(1) 直接定位，非整表扫描）。
func TestLookupProbesConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run("", func(t *testing.T) {
			tab := NewTable(2*m, 64)
			for i := 0; i < m; i++ {
				if err := tab.Insert(i * 2); err != nil { // 偶数键，低负载
					t.Fatalf("Insert(%d): %v", i*2, err)
				}
			}
			for _, x := range []int{0, 2 * (m - 1), 2*m + 1, -7} { // 存在与不存在
				tab.Lookup(x)
				if got := tab.lastProbe.Load(); got != 2 {
					t.Fatalf("m=%d Lookup(%d) probed %d slots, want 2", m, x, got)
				}
			}
		})
	}
}
