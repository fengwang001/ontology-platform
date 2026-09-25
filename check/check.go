// Package check 提供朴素参照实现（Bellman-Ford）与全部测试。依赖 dij。
package check

import (
	"math"
	"math/rand"

	"ontology/graph"
)

// Reference 用 Bellman-Ford 计算单源最短距离，作为 dij.ShortestPath 的对照。
// 调用方需保证输入合法（非负权、端点与 src 不越界）。
func Reference(n int, edges []graph.Edge, src int) []float64 {
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	for i := 0; i < n-1; i++ {
		for _, e := range edges {
			if nd := dist[e.U] + e.W; nd < dist[e.V] {
				dist[e.V] = nd
			}
		}
	}
	return dist
}

func randEdges(rng *rand.Rand, n, m int) []graph.Edge {
	batch := make([]graph.Edge, m)
	for i := range batch {
		batch[i] = graph.Edge{U: rng.Intn(n), V: rng.Intn(n), W: float64(rng.Intn(100))}
	}
	return batch
}

func es(ps ...[3]float64) []graph.Edge {
	out := make([]graph.Edge, len(ps))
	for i, p := range ps {
		out[i] = graph.Edge{U: int(p[0]), V: int(p[1]), W: p[2]}
	}
	return out
}

// staleEdges 中节点 2 的距离被改进两次（100->51->3），堆里留下两条过期记录。
var staleEdges = es([3]float64{0, 2, 100}, [3]float64{0, 1, 1}, [3]float64{1, 2, 50},
	[3]float64{0, 3, 2}, [3]float64{3, 2, 1}, [3]float64{2, 4, 1})

var staleWant = []float64{0, 1, 3, 2, 4}

// buggy 是内联的错误示范：不跳过过期记录，且用弹出的 d 覆盖式更新邻居。
func buggy(n int, edges []graph.Edge, src int) []float64 {
	type rec struct {
		v int
		d float64
	}
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	q := []rec{{src, 0}}
	for len(q) > 0 {
		b := 0
		for i := range q {
			if q[i].d < q[b].d {
				b = i
			}
		}
		cur := q[b]
		q = append(q[:b], q[b+1:]...)
		for _, e := range edges {
			if e.U == cur.v {
				dist[e.V] = cur.d + e.W // 错误：过期记录把邻居距离抬高
				q = append(q, rec{e.V, dist[e.V]})
			}
		}
	}
	return dist
}
