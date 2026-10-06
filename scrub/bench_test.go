package scrub

import (
	"fmt"
	"testing"
)

// BenchmarkSelectDue 验证到期选取的开销不随块总数线性增长：
// 固定 16 个到期块，块总数从 1k 增长到 100k，单次选取耗时应基本不变
// （堆操作只带 O(log n) 因子）。运行：go test -bench SelectDue -benchmem ./scrub
func BenchmarkSelectDue(b *testing.B) {
	const dueCount = 16
	for _, total := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("blocks=%d", total), func(b *testing.B) {
			s := NewService()
			for i := 0; i < total; i++ {
				if err := s.CreateBlock(uint64(i), []uint64{1, 2}, 1, "a", 1_000_000_000_000, 0); err != nil {
					b.Fatal(err)
				}
			}
			// 让绝大多数块在 t=0 巡检过且间隔极大（不到期），
			// 只留 dueCount 个从未巡检的块（总是到期）。
			for i := dueCount; i < total; i++ {
				if _, err := s.Scrub(uint64(i), 0, nil); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ids, err := s.SelectDue(100, dueCount)
				if err != nil || len(ids) != dueCount {
					b.Fatalf("ids=%d err=%v", len(ids), err)
				}
			}
		})
	}
}

// BenchmarkArbitrate 验证一次仲裁的开销只随该块副本数增长（副本数上限 5）。
func BenchmarkArbitrate(b *testing.B) {
	replicas := []Replica{
		{NodeID: 1, Version: 9, Stored: "x", Actual: "rot"},
		{NodeID: 2, Version: 7, Stored: "a", Actual: "a"},
		{NodeID: 3, Version: 7, Stored: "a", Actual: "a"},
		{NodeID: 4, Version: 5, Stored: "a", Actual: "a"},
		{NodeID: 5, Version: 5, Stored: "a", Actual: "rot"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := Arbitrate(replicas)
		if v.Outcome != OutcomeRepaired {
			b.Fatalf("outcome=%v", v.Outcome)
		}
	}
}
