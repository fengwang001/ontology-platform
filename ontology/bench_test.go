package ontology

import (
	"fmt"
	"testing"
)

// BenchmarkDeleteRestore 证明整体删除/复活的开销不随历史记录总数增长：
// go test -bench=DeleteRestore -benchtime=100x ./ontology
// 不同 N 下 ns/op 应保持同一量级。
func BenchmarkDeleteRestore(b *testing.B) {
	for _, n := range []int{100, 10000, 1000000} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			s := NewService()
			id := ObjectID("bench")
			if err := s.CreateObject(id); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err := s.AddHistory(id, "p", "v", int64(i+1)); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.DeleteObject(id); err != nil {
					b.Fatal(err)
				}
				if err := s.RestoreObject(id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
