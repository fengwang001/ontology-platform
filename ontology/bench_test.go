package ontology

import (
	"fmt"
	"testing"
)

// 基准：历史演变事件总数不同的实例，热路径重建耗时应近似恒定。
// 运行：go test -bench=BenchmarkRebuildWarm -benchtime=100x ./ontology
func BenchmarkRebuildWarm(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("evolutions=%d", n), func(b *testing.B) {
			schema := testSchema()
			store := NewEventStore()
			rb := NewRebuilder(store, schema, 64)
			const id = "inst"
			store.Append(create(id, 0, "A"))
			cur := "A"
			for i := 1; i <= n; i++ {
				next := "B"
				if cur == "B" {
					next = "A"
				}
				store.Append(evolve(id, int64(i), next))
				cur = next
			}
			if _, _, err := rb.Rebuild(id, int64(n)); err != nil { // 预热检查点
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := rb.Rebuild(id, int64(n)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
