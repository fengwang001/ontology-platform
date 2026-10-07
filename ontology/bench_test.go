package ontology

import (
	"fmt"
	"testing"
)

// setupAdjustableGroup 建立一个含 10 条链接的登记组，并预置 2 条待处理。
func setupAdjustableGroup(b *testing.B, adjustments int) *Manager {
	m := NewManager()
	if err := m.DefineLinkType("T", "A", "B", 10, Unlimited); err != nil {
		b.Fatal(err)
	}
	m.RegisterObject("root")
	for i := 0; i < 10; i++ {
		obj := fmt.Sprintf("t%d", i)
		m.RegisterObject(obj)
		if err := m.CreateLink("T", fmt.Sprintf("l%d", i), "root", obj); err != nil {
			b.Fatal(err)
		}
	}
	// 制造指定次数的历史基数调整（在 8 与 10 之间交替，
	// 期间始终有 0 或 2 条待处理）。
	for i := 0; i < adjustments; i++ {
		lim := 8
		if i%2 == 1 {
			lim = 10
		}
		if err := m.SetLimit("T", DirectionOut, lim); err != nil {
			b.Fatal(err)
		}
	}
	if err := m.SetLimit("T", DirectionOut, 8); err != nil {
		b.Fatal(err)
	}
	return m
}

// BenchmarkPendingCount 证明查询待处理数量的开销不随历史基数调整次数增长：
// 0 / 1k / 10k 次历史调整后，单次 PendingCount 的 ns/op 应保持同一量级
// （实现上只读取增量维护的计数，为 O(1)）。
func BenchmarkPendingCount(b *testing.B) {
	for _, adj := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("adjustments=%d", adj), func(b *testing.B) {
			m := setupAdjustableGroup(b, adj)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := m.PendingCount("T", DirectionOut, "root"); got != 2 {
					b.Fatalf("PendingCount = %d, want 2", got)
				}
			}
		})
	}
}

// TestPendingCountAfterManyAdjustments 正确性核对：大量历史调整后，
// 增量维护的待处理计数与全量扫描结果一致，且与调整次数无关。
func TestPendingCountAfterManyAdjustments(t *testing.T) {
	for _, adj := range []int{0, 100, 5000} {
		m := NewManager()
		if err := m.DefineLinkType("T", "A", "B", 10, Unlimited); err != nil {
			t.Fatal(err)
		}
		m.RegisterObject("root")
		for i := 0; i < 10; i++ {
			obj := fmt.Sprintf("t%d", i)
			m.RegisterObject(obj)
			if err := m.CreateLink("T", fmt.Sprintf("l%d", i), "root", obj); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < adj; i++ {
			lim := 8
			if i%2 == 1 {
				lim = 10
			}
			if err := m.SetLimit("T", DirectionOut, lim); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.SetLimit("T", DirectionOut, 8); err != nil {
			t.Fatal(err)
		}
		// 全量扫描核对。
		scan := 0
		for _, v := range m.QueryLinks("T", DirectionOut, "root") {
			if v.Status == StatusPending {
				scan++
			}
		}
		if got := m.PendingCount("T", DirectionOut, "root"); got != scan || got != 2 {
			t.Fatalf("adjustments=%d: PendingCount=%d, scan=%d, want 2", adj, got, scan)
		}
		stats := m.Stats("T", DirectionOut, "root")
		if stats.Adjustments != adj+1 {
			t.Fatalf("adjustments counter = %d, want %d", stats.Adjustments, adj+1)
		}
	}
}
