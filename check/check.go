// Package check 提供朴素参照实现与 dij 的正确性测试。
package check

import (
	"math"

	"ontology/graph"
)

// BellmanFord 是 O(n·m) 的朴素参照实现，不可达节点为 +Inf。
func BellmanFord(n int, edges []graph.Edge, src int) []float64 {
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	for i := 0; i < n-1; i++ {
		for _, e := range edges {
			if d := dist[e.From] + e.W; d < dist[e.To] {
				dist[e.To] = d
			}
		}
	}
	return dist
}
