package sw

import "testing"

// chainAdj builds the adjacency of a path 0-1-...-(m-1) with unit weights.
func chainAdj(m int) []map[int]int64 {
	adj := make([]map[int]int64, m)
	for i := 0; i+1 < m; i++ {
		for _, v := range []int{i, i + 1} {
			if adj[v] == nil {
				adj[v] = map[int]int64{}
			}
		}
		adj[i][i+1], adj[i+1][i] = 1, 1
	}
	return adj
}

// The number of candidate checks per MAS selection must stay bounded by
// a small constant independent of m (a heap is used, not a full scan).
func TestMASCheckedCandidatesBounded(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		adj := chainAdj(m)
		alive := make([]bool, m)
		for i := range alive {
			alive[i] = true
		}
		var c masCounter
		mas(adj, alive, &c)
		if c.maxChecked > 2 {
			t.Errorf("m=%d: one selection checked %d candidates, want <= 2", m, c.maxChecked)
		}
	}
}

// Merging must sum the weights of edges to common neighbors.
func TestMergeSumsCommonNeighbor(t *testing.T) {
	adj := []map[int]int64{
		0: {1: 3, 2: 5},
		1: {0: 3, 2: 2},
		2: {0: 5, 1: 2, 3: 1},
		3: {2: 1},
	}
	mergeInto(adj, 1, 3)            // common neighbor of 1 and 3 is node 2
	if got := adj[1][2]; got != 3 { // 2 + 1, not overwritten
		t.Fatalf("merged edge (1,2) = %d, want 3", got)
	}
	if got := adj[2][1]; got != 3 {
		t.Fatalf("merged edge (2,1) = %d, want 3", got)
	}
	if got := adj[1][0]; got != 3 {
		t.Fatalf("edge (1,0) = %d, want 3 (untouched)", got)
	}
	if adj[3] != nil {
		t.Fatalf("node 3 still present after merge")
	}
}
