package traverse

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"ontology/graph"
)

// BenchmarkTraverseLimitMagnitude 固定图与遍历规模，仅放大条数上限数值，
// 验证簿记开销不随上限数值增长（ns/op 应保持平坦）。
func BenchmarkTraverseLimitMagnitude(b *testing.B) {
	const K = 2000
	s := graph.NewStore()
	_ = s.AddObject(graph.Object{ID: "a"})
	for i := 0; i < K; i++ {
		id := fmt.Sprintf("x%05d", i)
		_ = s.AddObject(graph.Object{ID: id})
		_ = s.AddLink(graph.Link{ID: fmt.Sprintf("l%05d", i), Type: "t", SourceID: "a", TargetID: id})
	}
	svc := NewService(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, n := range []int{K, 100 * K, 10000 * K} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := svc.Traverse(Request{
					StartID:     "a",
					Directions:  []graph.Direction{graph.Outgoing},
					DepthLimit:  5,
					ResultLimit: n,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
