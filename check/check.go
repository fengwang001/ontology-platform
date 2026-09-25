// Package check provides a naive Bellman-Ford reference implementation.
package check

import (
	"math"

	"ontology/graph"
)

// Naive returns shortest distances from src by n-1 rounds of full relaxation.
func Naive(n int, edges []graph.Edge, src int) []float64 {
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
