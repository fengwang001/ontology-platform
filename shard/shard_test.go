package shard

import (
	"fmt"
	"testing"
)

// TestCheckedKeysConstant 证明「判定键是否已迁移」用哈希定位而非线性扫描：
// 先让 m 个不同键成为已迁移热点键，再检查一个新键与已迁移键，
// 检查的键个数不随 m 增长（每事件恰好 1 次）。
func TestCheckedKeysConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			e := New(4, 2)
			for i := 0; i < m; i++ {
				key := fmt.Sprintf("hot-%d", i)
				for j := 0; j < e.T; j++ { // 模拟该键在基础分片上累计到阈值
					e.ObserveBase(key)
				}
				e.MarkHot(key)
			}
			before := e.checked
			if e.Migrated("brand-new-key") { // 非热点新键：一次哈希定位
				t.Fatal("new key must not be migrated")
			}
			if got := e.checked - before; got != 1 {
				t.Fatalf("checked keys for new key = %d, want 1 (m=%d)", got, m)
			}
			before = e.checked
			if !e.Migrated("hot-0") { // 已迁移键：同样一次哈希定位
				t.Fatal("hot-0 must be migrated")
			}
			if got := e.checked - before; got != 1 {
				t.Fatalf("checked keys for migrated key = %d, want 1 (m=%d)", got, m)
			}
		})
	}
}

// TestHotThreshold 表驱动钉住热点判定：累计数 >= T 才触发，触发后永久保持。
func TestHotThreshold(t *testing.T) {
	cases := []struct {
		T       int
		feeds   int
		wantHot bool
	}{
		{3, 2, false},
		{3, 3, true},
		{3, 5, true},
		{1, 1, true},
	}
	for _, c := range cases {
		e := New(2, c.T)
		reached := false
		for i := 0; i < c.feeds; i++ {
			if e.ObserveBase("k") {
				reached = true
			}
		}
		if reached != c.wantHot {
			t.Fatalf("T=%d feeds=%d reached=%v want %v", c.T, c.feeds, reached, c.wantHot)
		}
	}
}
